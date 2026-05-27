package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/ssoriche/terraform-provider-kanidm/internal/client"
)

var (
	_ resource.Resource                = (*groupResource)(nil)
	_ resource.ResourceWithImportState = (*groupResource)(nil)
)

func NewGroupResource() resource.Resource {
	return &groupResource{}
}

type groupResource struct {
	client *client.Client
}

type groupResourceModel struct {
	Name        types.String `tfsdk:"name"`
	ID          types.String `tfsdk:"id"`
	Description types.String `tfsdk:"description"`
	Members     types.Set    `tfsdk:"members"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm group.

Groups are used to organize users and service accounts, and control access to resources.

## Example Usage

` + "```hcl" + `
resource "kanidm_group" "developers" {
  name        = "developers"
  description = "Development team members"

  members = [
    kanidm_person.alice.id,
    kanidm_person.bob.id,
    kanidm_service_account.ci.id,
  ]
}
` + "```" + ``,

		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Group name. This can be changed outside Terraform, but is tracked via the stable UUID in `id`.",
				Required:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Stable Kanidm UUID for this group. This value is computed after creation/import and used to keep the resource linked across external renames.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description of the group.",
				Optional:            true,
			},
			"members": schema.SetAttribute{
				MarkdownDescription: "Set of member IDs (persons or service accounts). " +
					"Members are managed as a complete set - any changes will replace all members.",
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data := configureResource(req, resp)
	if data == nil {
		return
	}

	r.client = data.client
}

func (r *groupResource) normalizeMemberIdentifiers(ctx context.Context, members []string) ([]string, error) {
	if len(members) == 0 {
		return []string{}, nil
	}

	normalized := make([]string, 0, len(members))

	for _, member := range members {
		person, err := r.client.GetPerson(ctx, member)
		if err == nil {
			if person.UUID == "" {
				return nil, fmt.Errorf("person %q did not return a UUID", member)
			}
			normalized = append(normalized, person.UUID)
			continue
		}
		if !errors.Is(err, client.ErrNotFound) {
			return nil, fmt.Errorf("resolve person member %q: %w", member, err)
		}

		serviceAccount, err := r.client.GetServiceAccount(ctx, member)
		if err == nil {
			normalized = append(normalized, serviceAccount.Name)
			continue
		}
		if !errors.Is(err, client.ErrNotFound) {
			return nil, fmt.Errorf("resolve service account member %q: %w", member, err)
		}

		normalized = append(normalized, member)
	}

	return normalized, nil
}

func (r *groupResource) applyGroupState(ctx context.Context, model *groupResourceModel, group *client.Group) error {
	if group.UUID == "" {
		return errors.New("Kanidm did not return a UUID for the requested group")
	}

	model.ID = types.StringValue(group.UUID)
	model.Name = types.StringValue(group.Name)
	model.Description = types.StringValue(group.Description)

	normalizedMembers, err := r.normalizeMemberIdentifiers(ctx, group.Members)
	if err != nil {
		return err
	}

	membersSet, diags := types.SetValueFrom(ctx, types.StringType, normalizedMembers)
	if diags.HasError() {
		return fmt.Errorf("convert members set: %s", diags.Errors()[0].Summary())
	}
	model.Members = membersSet

	return nil
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Creating group", map[string]any{
		"name": plan.Name.ValueString(),
	})

	// Create the group
	description := ""
	if !plan.Description.IsNull() {
		description = plan.Description.ValueString()
	}

	group, err := r.client.CreateGroup(ctx, plan.Name.ValueString(), description)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Group",
			"Could not create group: "+err.Error(),
		)
		return
	}

	// Add members if provided
	if !plan.Members.IsNull() && !plan.Members.IsUnknown() {
		var memberIDs []string
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &memberIDs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		if len(memberIDs) > 0 {
			tflog.Debug(ctx, "Adding members to group", map[string]any{
				"count": len(memberIDs),
			})
			if err := r.client.UpdateGroup(ctx, group.Name, nil, nil, memberIDs); err != nil {
				resp.Diagnostics.AddError(
					"Error Adding Members",
					"Group was created but members could not be added: "+err.Error(),
				)
				return
			}
		}
	}

	// Read back the created group
	createdGroup, err := r.client.GetGroup(ctx, group.Name)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Group was created but could not be read back: "+err.Error(),
		)
		return
	}

	if err := r.applyGroupState(ctx, &plan, createdGroup); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Group was created but could not be mapped back to Terraform state: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "Group created successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading group", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Get current group from API
	group, err := r.client.GetGroup(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "Group not found, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Could not read group: "+err.Error(),
		)
		return
	}

	if err := r.applyGroupState(ctx, &state, group); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Could not map group data into Terraform state: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Updating group", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Prepare members list
	var memberIDs []string
	if !plan.Members.IsNull() && !plan.Members.IsUnknown() {
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &memberIDs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	nameChanged := !plan.Name.Equal(state.Name)
	descriptionChanged := !plan.Description.Equal(state.Description)

	var name *string
	if nameChanged {
		newName := plan.Name.ValueString()
		name = &newName
	}

	var description *string
	if descriptionChanged {
		newDescription := ""
		if !plan.Description.IsNull() {
			newDescription = plan.Description.ValueString()
		}
		description = &newDescription
	}

	if err := r.client.UpdateGroup(ctx, state.ID.ValueString(), name, description, memberIDs); err != nil {
		resp.Diagnostics.AddError(
			"Error Updating Group",
			"Could not update group: "+err.Error(),
		)
		return
	}

	// Read back the updated group
	updatedGroup, err := r.client.GetGroup(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Group was updated but could not be read back: "+err.Error(),
		)
		return
	}

	if err := r.applyGroupState(ctx, &plan, updatedGroup); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Group was updated but could not be mapped back to Terraform state: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "Group updated successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Deleting group", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Delete the group
	if err := r.client.DeleteGroup(ctx, state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "Group not found during delete, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting Group",
			"Could not delete group: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "Group deleted successfully", map[string]any{
		"id": state.ID.ValueString(),
	})
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Use the ID (group name) directly as the import identifier
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)

	tflog.Debug(ctx, "Imported group", map[string]any{
		"id": req.ID,
	})
}
