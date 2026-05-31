package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/ssoriche/terraform-provider-kanidm/internal/client"
)

var (
	_ resource.Resource                = (*groupResource)(nil)
	_ resource.ResourceWithImportState = (*groupResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*groupResource)(nil)
	_ resource.ResourceWithUpgradeState = (*groupResource)(nil)
)

func NewGroupResource() resource.Resource {
	return &groupResource{}
}

type groupResource struct {
	client *client.Client
}

type groupResourceModel struct {
	Name           types.String `tfsdk:"name"`
	ID             types.String `tfsdk:"id"`
	Description    types.String `tfsdk:"description"`
	Mail           types.List   `tfsdk:"mail"`
	PosixEnabled   types.Bool   `tfsdk:"posix_enabled"`
	GIDNumber      types.Int64  `tfsdk:"gidnumber"`
	EntryManagedBy types.String `tfsdk:"entry_managed_by"`
	Members        types.Set    `tfsdk:"members"`
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
			"mail": schema.ListAttribute{
				MarkdownDescription: "Email addresses for the group.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"posix_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether POSIX support is enabled for the group. Enabling without a gidnumber lets Kanidm generate one automatically. Disabling after enablement is not currently supported.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"gidnumber": schema.Int64Attribute{
				MarkdownDescription: "Optional POSIX gidnumber for the group. Computed after POSIX is enabled, even when Kanidm generates the value.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"entry_managed_by": schema.StringAttribute{
				MarkdownDescription: "Account or group ID that can manage this group. Use UUIDs or fully-qualified names.",
				Optional:            true,
			},
"members": schema.SetAttribute{
				MarkdownDescription: "Set of member IDs (persons, service accounts, or groups). " +
					"Members are managed as a complete set - any changes will replace all members. " +
					"Use UUIDs or fully-qualified names (e.g., `user@domain.com`).",
				Optional:    true,
				Computed:    true,
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

func (r *groupResource) resolveManagerSPN(ctx context.Context, identifier string) (string, error) {
	if identifier == "" {
		return "", nil
	}
	if person, err := r.client.GetPerson(ctx, identifier); err == nil {
		if person.SPN != "" {
			return person.SPN, nil
		}
		if person.Name != "" {
			return person.Name, nil
		}
	} else if !errors.Is(err, client.ErrNotFound) {
		return "", err
	}
	if serviceAccount, err := r.client.GetServiceAccount(ctx, identifier); err == nil {
		if serviceAccount.SPN != "" {
			return serviceAccount.SPN, nil
		}
		if serviceAccount.Name != "" {
			return serviceAccount.Name, nil
		}
	} else if !errors.Is(err, client.ErrNotFound) {
		return "", err
	}
	if group, err := r.client.GetGroup(ctx, identifier); err == nil {
		if group.SPN != "" {
			return group.SPN, nil
		}
		if group.Name != "" {
			return group.Name, nil
		}
	} else if !errors.Is(err, client.ErrNotFound) {
		return "", err
	}
	return "", client.ErrNotFound
}

func (r *groupResource) resolveManagerUUID(ctx context.Context, identifier string) (string, error) {
	if identifier == "" {
		return "", nil
	}
	if person, err := r.client.GetPerson(ctx, identifier); err == nil {
		if person.UUID != "" {
			return person.UUID, nil
		}
	} else if !errors.Is(err, client.ErrNotFound) {
		return "", err
	}
	if serviceAccount, err := r.client.GetServiceAccount(ctx, identifier); err == nil {
		if serviceAccount.UUID != "" {
			return serviceAccount.UUID, nil
		}
	} else if !errors.Is(err, client.ErrNotFound) {
		return "", err
	}
	if group, err := r.client.GetGroup(ctx, identifier); err == nil {
		if group.UUID != "" {
			return group.UUID, nil
		}
	} else if !errors.Is(err, client.ErrNotFound) {
		return "", err
	}
	return "", client.ErrNotFound
}

func (r *groupResource) normalizeEntryManagedBy(ctx context.Context, managers []string) ([]string, error) {
	if len(managers) == 0 {
		return []string{}, nil
	}
	normalized := make([]string, 0, len(managers))
	for _, manager := range managers {
		uuid, err := r.resolveManagerUUID(ctx, manager)
		if err == nil {
			normalized = append(normalized, uuid)
			continue
		}
		if errors.Is(err, client.ErrNotFound) {
			normalized = append(normalized, manager)
			continue
		}
		return nil, err
	}
	return normalized, nil
}

func (r *groupResource) resolveEntryManagedBySPNs(ctx context.Context, managers []string) ([]string, error) {
	if len(managers) == 0 {
		return []string{}, nil
	}
	resolved := make([]string, 0, len(managers))
	for _, manager := range managers {
		spn, err := r.resolveManagerSPN(ctx, manager)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, spn)
	}
	return resolved, nil
}

func (r *groupResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
}

func (r *groupResource) normalizeMemberIdentifiers(ctx context.Context, members []string) ([]string, error) {
	if len(members) == 0 {
		return []string{}, nil
	}

	normalized := make([]string, 0, len(members))

	for _, member := range members {
		person, err := r.client.GetPerson(ctx, member)
		if err == nil {
			if person.UUID != "" {
				normalized = append(normalized, person.UUID)
				continue
			}
		} else if !errors.Is(err, client.ErrNotFound) {
			return nil, fmt.Errorf("resolve person member %q: %w", member, err)
		}

		serviceAccount, err := r.client.GetServiceAccount(ctx, member)
		if err == nil {
			if serviceAccount.UUID != "" {
				normalized = append(normalized, serviceAccount.UUID)
				continue
			}
		} else if !errors.Is(err, client.ErrNotFound) {
			return nil, fmt.Errorf("resolve service account member %q: %w", member, err)
		}

		group, err := r.client.GetGroup(ctx, member)
		if err == nil {
			if group.UUID != "" {
				normalized = append(normalized, group.UUID)
				continue
			}
		} else if !errors.Is(err, client.ErrNotFound) {
			return nil, fmt.Errorf("resolve group member %q: %w", member, err)
		}

		normalized = append(normalized, member)
	}

	return normalized, nil
}

func (r *groupResource) resolveMemberSPNs(ctx context.Context, members []string) ([]string, error) {
	if len(members) == 0 {
		return []string{}, nil
	}
	resolved := make([]string, 0, len(members))
	for _, member := range members {
		spn, err := r.resolveManagerSPN(ctx, member)
		if err == nil {
			resolved = append(resolved, spn)
			continue
		}
		if errors.Is(err, client.ErrNotFound) {
			resolved = append(resolved, member)
			continue
		}
		return nil, err
	}
	return resolved, nil
}

func (r *groupResource) applyGroupState(ctx context.Context, model *groupResourceModel, group *client.Group) error {
	if group.UUID == "" {
		return errors.New("kanidm did not return a UUID for the requested group")
	}

	model.ID = types.StringValue(group.UUID)
	if group.Name != "" {
		model.Name = types.StringValue(group.Name)
	} else if model.Name.IsNull() || model.Name.IsUnknown() {
		model.Name = types.StringValue(group.Name)
	}
	if group.Description != "" {
		model.Description = types.StringValue(group.Description)
	} else if model.Description.IsNull() || model.Description.IsUnknown() {
		model.Description = types.StringNull()
	}
	if len(group.Mail) > 0 {
		mailList, diags := types.ListValueFrom(ctx, types.StringType, group.Mail)
		if diags.HasError() {
			return fmt.Errorf("convert mail list: %s", diags.Errors()[0].Summary())
		}
		model.Mail = mailList
	} else {
		model.Mail = types.ListNull(types.StringType)
	}
	if group.PosixEnabled {
		model.PosixEnabled = types.BoolValue(true)
		if group.GIDNumber == nil {
			if unixToken, err := r.client.GetGroupUnixToken(ctx, group.UUID); err == nil {
				group.GIDNumber = &unixToken.GIDNumber
			}
		}
		if group.GIDNumber != nil {
			model.GIDNumber = types.Int64Value(*group.GIDNumber)
		} else {
			model.GIDNumber = types.Int64Null()
		}
	} else {
		model.PosixEnabled = types.BoolValue(false)
		model.GIDNumber = types.Int64Null()
	}
	if len(group.EntryManagedBy) > 0 {
		apiEmbyUUID, err := r.resolveManagerUUID(ctx, group.EntryManagedBy[0])
		if err != nil || apiEmbyUUID == "" {
			apiEmbyUUID = group.EntryManagedBy[0]
		}
		if !model.EntryManagedBy.IsNull() && !model.EntryManagedBy.IsUnknown() {
			modelEmbyUUID, err := r.resolveManagerUUID(ctx, model.EntryManagedBy.ValueString())
			if err == nil && modelEmbyUUID == apiEmbyUUID {
				// keep model's format
			} else {
				model.EntryManagedBy = types.StringValue(apiEmbyUUID)
			}
		} else {
			model.EntryManagedBy = types.StringValue(apiEmbyUUID)
		}
	} else {
		model.EntryManagedBy = types.StringNull()
	}

	if len(group.Members) > 0 {
		apiUUIDs, err := r.normalizeMemberIdentifiers(ctx, group.Members)
		if err != nil {
			return fmt.Errorf("resolve members to UUIDs: %w", err)
		}
		apiMemberMap := make(map[string]string, len(apiUUIDs))
		for i, spn := range group.Members {
			apiMemberMap[apiUUIDs[i]] = spn
		}
		if !model.Members.IsNull() && !model.Members.IsUnknown() {
			var existingMembers []string
			respDiags := model.Members.ElementsAs(ctx, &existingMembers, false)
			if respDiags.HasError() {
				return fmt.Errorf("extract existing members: %s", respDiags.Errors()[0].Summary())
			}
			existingUUIDs, err := r.normalizeMemberIdentifiers(ctx, existingMembers)
			if err == nil {
				modelUUIDToValue := make(map[string]string, len(existingMembers))
				for i, v := range existingMembers {
					if i < len(existingUUIDs) {
						modelUUIDToValue[existingUUIDs[i]] = v
					}
				}
				finalMembers := make([]string, len(apiUUIDs))
				for i, uuid := range apiUUIDs {
					if preserved, ok := modelUUIDToValue[uuid]; ok {
						finalMembers[i] = preserved
					} else {
						finalMembers[i] = uuid
					}
				}
				membersSet, diags := types.SetValueFrom(ctx, types.StringType, finalMembers)
				if diags.HasError() {
					return fmt.Errorf("convert members set: %s", diags.Errors()[0].Summary())
				}
				model.Members = membersSet
			} else {
				membersSet, diags := types.SetValueFrom(ctx, types.StringType, apiMemberMap)
				if diags.HasError() {
					return fmt.Errorf("convert members set: %s", diags.Errors()[0].Summary())
				}
				model.Members = membersSet
			}
		} else {
			finalMembers := make([]string, len(apiUUIDs))
			for i, uuid := range apiUUIDs {
				finalMembers[i] = uuid
			}
			membersSet, diags := types.SetValueFrom(ctx, types.StringType, finalMembers)
			if diags.HasError() {
				return fmt.Errorf("convert members set: %s", diags.Errors()[0].Summary())
			}
			model.Members = membersSet
		}
	} else {
		model.Members = types.SetNull(types.StringType)
	}

	return nil
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	posixEnabled := (!plan.PosixEnabled.IsNull() && !plan.PosixEnabled.IsUnknown() && plan.PosixEnabled.ValueBool()) || (!plan.GIDNumber.IsNull() && !plan.GIDNumber.IsUnknown())
	if !plan.PosixEnabled.IsNull() && !plan.PosixEnabled.IsUnknown() && !plan.PosixEnabled.ValueBool() && !plan.GIDNumber.IsNull() && !plan.GIDNumber.IsUnknown() {
		resp.Diagnostics.AddError("Invalid POSIX Configuration", "gidnumber requires posix_enabled = true.")
		return
	}

	tflog.Debug(ctx, "Creating group", map[string]any{
		"name": plan.Name.ValueString(),
	})

	// Create the group
	description := ""
	var mail []string
	var entryManagedBy []string
	var err error
	if !plan.Description.IsNull() {
		description = plan.Description.ValueString()
	}
	if !plan.EntryManagedBy.IsNull() && !plan.EntryManagedBy.IsUnknown() {
		entryManagedByStr := plan.EntryManagedBy.ValueString()
		embySPN, err := r.resolveManagerSPN(ctx, entryManagedByStr)
		if err != nil {
			resp.Diagnostics.AddError("Error Resolving entry_managed_by", err.Error())
			return
		}
		entryManagedBy = []string{embySPN}
	}
	if !plan.Mail.IsNull() && !plan.Mail.IsUnknown() {
		resp.Diagnostics.Append(plan.Mail.ElementsAs(ctx, &mail, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	group, err := r.client.CreateGroup(ctx, plan.Name.ValueString(), description)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Group",
			"Could not create group: "+err.Error(),
		)
		return
	}

	if entryManagedBy != nil {
		if err := r.client.UpdateGroup(ctx, group.Name, nil, nil, nil, entryManagedBy, nil); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Managed By",
				"Group was created but entry_managed_by could not be set: "+err.Error(),
			)
			return
		}
	}
	if mail != nil {
		if err := r.client.UpdateGroup(ctx, group.Name, nil, nil, mail, nil, nil); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Mail",
				"Group was created but mail could not be set: "+err.Error(),
			)
			return
		}
	}
	if posixEnabled {
		var gid *int64
		if !plan.GIDNumber.IsNull() && !plan.GIDNumber.IsUnknown() {
			value := plan.GIDNumber.ValueInt64()
			gid = &value
		}
		if err := r.client.SetGroupGIDNumber(ctx, group.Name, gid); err != nil {
			resp.Diagnostics.AddError(
				"Error Enabling POSIX Group",
				"Group was created but POSIX support could not be enabled: "+err.Error(),
			)
			return
		}
	}

	// Add members if provided
	if !plan.Members.IsNull() && !plan.Members.IsUnknown() {
		var memberIDs []string
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &memberIDs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		resolved, err := r.resolveMemberSPNs(ctx, memberIDs)
		if err != nil {
			resp.Diagnostics.AddError("Error Resolving members", err.Error())
			return
		}

		if len(resolved) > 0 {
			tflog.Debug(ctx, "Adding members to group", map[string]any{
				"count": len(resolved),
			})
			if err := r.client.UpdateGroup(ctx, group.Name, nil, nil, nil, nil, resolved); err != nil {
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
	if posixEnabled {
		if unixToken, err := r.client.GetGroupUnixToken(ctx, createdGroup.UUID); err == nil {
			createdGroup.PosixEnabled = true
			createdGroup.GIDNumber = &unixToken.GIDNumber
		}
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

	// Prepare members list - resolve to SPNs for API
	var memberIDs []string
	var mail []string
	var entryManagedBy []string
	var err error
	if !plan.Members.IsNull() && !plan.Members.IsUnknown() {
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &memberIDs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		memberIDs, err = r.resolveMemberSPNs(ctx, memberIDs)
		if err != nil {
			resp.Diagnostics.AddError("Error Resolving members", err.Error())
			return
		}
	}
	if !plan.EntryManagedBy.IsNull() && !plan.EntryManagedBy.IsUnknown() {
		embySPN, err := r.resolveManagerSPN(ctx, plan.EntryManagedBy.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error Resolving entry_managed_by", err.Error())
			return
		}
		entryManagedBy = []string{embySPN}
	}
	if !plan.Mail.IsNull() && !plan.Mail.IsUnknown() {
		resp.Diagnostics.Append(plan.Mail.ElementsAs(ctx, &mail, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	nameChanged := !plan.Name.Equal(state.Name)
	descriptionChanged := !plan.Description.Equal(state.Description)
	mailChanged := !plan.Mail.Equal(state.Mail)
	posixChanged := !plan.PosixEnabled.Equal(state.PosixEnabled)
	gidNumberChanged := !plan.GIDNumber.Equal(state.GIDNumber)

	// Compare entry_managed_by by resolving both sides to UUIDs
	entryManagedByChanged := false
	var planEmbyUUID, stateEmbyUUID string
	if !plan.EntryManagedBy.IsNull() && !plan.EntryManagedBy.IsUnknown() {
		planEmbyUUID, _ = r.resolveManagerUUID(ctx, plan.EntryManagedBy.ValueString())
		if planEmbyUUID == "" {
			planEmbyUUID = plan.EntryManagedBy.ValueString()
		}
	}
	if !state.EntryManagedBy.IsNull() && !state.EntryManagedBy.IsUnknown() {
		stateEmbyUUID, _ = r.resolveManagerUUID(ctx, state.EntryManagedBy.ValueString())
		if stateEmbyUUID == "" {
			stateEmbyUUID = state.EntryManagedBy.ValueString()
		}
	}
	if plan.EntryManagedBy.IsNull() && state.EntryManagedBy.IsNull() {
		entryManagedByChanged = false
	} else if plan.EntryManagedBy.IsNull() != state.EntryManagedBy.IsNull() {
		entryManagedByChanged = true
	} else {
		entryManagedByChanged = planEmbyUUID != stateEmbyUUID
	}

	// Compare members by resolving both sides to UUIDs
	membersChanged := false
	var planMembers, stateMembers []string
	if !plan.Members.IsNull() && !plan.Members.IsUnknown() {
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &planMembers, false)...)
	}
	if !state.Members.IsNull() && !state.Members.IsUnknown() {
		resp.Diagnostics.Append(state.Members.ElementsAs(ctx, &stateMembers, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	planMembersUUIDs, err := r.normalizeMemberIdentifiers(ctx, planMembers)
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving plan members", err.Error())
		return
	}
	stateMembersUUIDs, err := r.normalizeMemberIdentifiers(ctx, stateMembers)
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving state members", err.Error())
		return
	}
	planMembersSet, diags := types.SetValueFrom(ctx, types.StringType, planMembersUUIDs)
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	stateMembersSet, diags := types.SetValueFrom(ctx, types.StringType, stateMembersUUIDs)
	if diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}
	membersChanged = !planMembersSet.Equal(stateMembersSet)
	desiredPosixEnabled := (!plan.PosixEnabled.IsNull() && !plan.PosixEnabled.IsUnknown() && plan.PosixEnabled.ValueBool()) || (!plan.GIDNumber.IsNull() && !plan.GIDNumber.IsUnknown())
	currentPosixEnabled := !state.PosixEnabled.IsNull() && !state.PosixEnabled.IsUnknown() && state.PosixEnabled.ValueBool()
	if !plan.PosixEnabled.IsNull() && !plan.PosixEnabled.IsUnknown() && !plan.PosixEnabled.ValueBool() && !plan.GIDNumber.IsNull() && !plan.GIDNumber.IsUnknown() {
		resp.Diagnostics.AddError("Invalid POSIX Configuration", "gidnumber requires posix_enabled = true.")
		return
	}
	if currentPosixEnabled && !desiredPosixEnabled {
		resp.Diagnostics.AddError("Unsupported POSIX Update", "Disabling group POSIX support is not currently supported by this provider.")
		return
	}

	var name *string
	if nameChanged {
		newName := plan.Name.ValueString()
		name = &newName
	}

	var description *string
	if descriptionChanged {
		if !plan.Description.IsNull() {
			newDescription := plan.Description.ValueString()
			description = &newDescription
		} else {
			description = nil
		}
	}

	var mailToApply []string
	if mailChanged {
		if plan.Mail.IsNull() || plan.Mail.IsUnknown() {
			mailToApply = []string{}
		} else {
			mailToApply = mail
		}
	} else {
		mailToApply = nil
	}
	var managedBy []string
	if entryManagedByChanged {
		managedBy = entryManagedBy
	} else {
		managedBy = nil
	}
	var membersToApply []string
	if membersChanged {
		membersToApply = memberIDs
	} else {
		membersToApply = nil
	}
	if name == nil && description == nil && mailToApply == nil && managedBy == nil && membersToApply == nil && !posixChanged && !gidNumberChanged {
		tflog.Debug(ctx, "No changes to apply, skipping update", map[string]any{
			"id": state.ID.ValueString(),
		})
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	} else if err := r.client.UpdateGroup(ctx, state.ID.ValueString(), name, description, mailToApply, managedBy, membersToApply); err != nil {
		resp.Diagnostics.AddError(
			"Error Updating Group",
			"Could not update group: "+err.Error(),
		)
		return
	}
	if (posixChanged || gidNumberChanged) && (desiredPosixEnabled || currentPosixEnabled) && !plan.GIDNumber.IsUnknown() {
		if !plan.GIDNumber.IsNull() {
			value := plan.GIDNumber.ValueInt64()
			if err := r.client.SetGroupGIDNumber(ctx, state.ID.ValueString(), &value); err != nil {
				resp.Diagnostics.AddError(
					"Error Updating POSIX Group",
					"Could not update POSIX settings: "+err.Error(),
				)
				return
			}
		} else if gidNumberChanged {
			if err := r.client.DeleteGroupAttr(ctx, state.ID.ValueString(), "gidnumber"); err != nil {
				resp.Diagnostics.AddError(
					"Error Removing GID Number",
					"Could not remove explicit gidnumber: "+err.Error(),
				)
				return
			}
		}
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
	if desiredPosixEnabled || currentPosixEnabled {
		if unixToken, err := r.client.GetGroupUnixToken(ctx, updatedGroup.UUID); err == nil {
			updatedGroup.PosixEnabled = true
			updatedGroup.GIDNumber = &unixToken.GIDNumber
		}
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
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)

	tflog.Debug(ctx, "Imported group", map[string]any{
		"id": req.ID,
	})
}

func (r *groupResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"name":              schema.StringAttribute{Required: true},
					"id":                schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
					"description":       schema.StringAttribute{Optional: true},
					"mail":              schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType},
					"posix_enabled":     schema.BoolAttribute{Optional: true, Computed: true},
					"gidnumber":         schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}},
					"entry_managed_by":  schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType},
					"members":           schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var priorState struct {
					Name           types.String `tfsdk:"name"`
					ID             types.String `tfsdk:"id"`
					Description    types.String `tfsdk:"description"`
					Mail           types.List   `tfsdk:"mail"`
					PosixEnabled   types.Bool   `tfsdk:"posix_enabled"`
					GIDNumber      types.Int64  `tfsdk:"gidnumber"`
					EntryManagedBy types.Set    `tfsdk:"entry_managed_by"`
					Members        types.Set    `tfsdk:"members"`
				}

				resp.Diagnostics.Append(req.State.Get(ctx, &priorState)...)
				if resp.Diagnostics.HasError() {
					return
				}

				var embyValues []string
				resp.Diagnostics.Append(priorState.EntryManagedBy.ElementsAs(ctx, &embyValues, false)...)
				if resp.Diagnostics.HasError() {
					return
				}

				embyString := types.StringNull()
				if len(embyValues) > 0 {
					embyString = types.StringValue(embyValues[0])
				}

				upgradedState := groupResourceModel{
					Name:           priorState.Name,
					ID:             priorState.ID,
					Description:    priorState.Description,
					Mail:           priorState.Mail,
					PosixEnabled:   priorState.PosixEnabled,
					GIDNumber:      priorState.GIDNumber,
					EntryManagedBy: embyString,
					Members:        priorState.Members,
				}

				resp.Diagnostics.Append(resp.State.Set(ctx, &upgradedState)...)
			},
		},
	}
}
