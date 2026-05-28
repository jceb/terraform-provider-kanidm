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
	_ resource.Resource                = (*applicationResource)(nil)
	_ resource.ResourceWithImportState = (*applicationResource)(nil)
)

func NewApplicationResource() resource.Resource {
	return &applicationResource{}
}

type applicationResource struct {
	client *client.Client
}

type applicationResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	DisplayName types.String `tfsdk:"displayname"`
	LinkedGroup types.String `tfsdk:"linked_group"`
}

func (r *applicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application"
}

func (r *applicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm application entry.

Applications are used for per-user application passwords in legacy LDAP/basic-auth style integrations. Each application links to a single Kanidm group; only members of that group may mint application-specific credentials for the app.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Stable Kanidm UUID for this application.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Unique application name.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"displayname": schema.StringAttribute{
				MarkdownDescription: "Display name for the application.",
				Required:            true,
			},
			"linked_group": schema.StringAttribute{
				MarkdownDescription: "Group UUID or name whose members may mint application passwords for this application.",
				Required:            true,
			},
		},
	}
}

func (r *applicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data := configureResource(req, resp)
	if data == nil {
		return
	}
	r.client = data.client
}

func (r *applicationResource) resolveGroupUUID(ctx context.Context, identifier string) (string, error) {
	group, err := r.client.GetGroup(ctx, identifier)
	if err != nil {
		return "", fmt.Errorf("resolve group %q: %w", identifier, err)
	}
	if group.UUID == "" {
		return "", fmt.Errorf("group %q did not return a UUID", identifier)
	}
	return group.UUID, nil
}

func (r *applicationResource) syncState(ctx context.Context, model *applicationResourceModel, app *client.Application) error {
	model.ID = types.StringValue(app.ID)
	model.Name = types.StringValue(app.Name)
	model.DisplayName = types.StringValue(app.DisplayName)
	if app.LinkedGroup == "" {
		model.LinkedGroup = types.StringNull()
		return nil
	}
	groupUUID, err := r.resolveGroupUUID(ctx, app.LinkedGroup)
	if err != nil {
		return fmt.Errorf("resolve application linked group %q: %w", app.LinkedGroup, err)
	}
	model.LinkedGroup = types.StringValue(groupUUID)
	return nil
}

func (r *applicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan applicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	linkedGroupUUID, err := r.resolveGroupUUID(ctx, plan.LinkedGroup.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving Linked Group", err.Error())
		return
	}

	tflog.Debug(ctx, "Creating application", map[string]any{"name": plan.Name.ValueString()})
	app, err := r.client.CreateApplication(ctx, plan.Name.ValueString(), plan.DisplayName.ValueString(), linkedGroupUUID)
	if err != nil {
		resp.Diagnostics.AddError("Error Creating Application", err.Error())
		return
	}

	if err := r.syncState(ctx, &plan, app); err != nil {
		resp.Diagnostics.AddError("Error Reading Application", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state applicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lookupID := state.ID.ValueString()
	if lookupID == "" {
		lookupID = state.Name.ValueString()
	}
	app, err := r.client.GetApplication(ctx, lookupID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Application", err.Error())
		return
	}

	if err := r.syncState(ctx, &state, app); err != nil {
		resp.Diagnostics.AddError("Error Reading Application", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *applicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state applicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var displayName *string
	if !plan.DisplayName.Equal(state.DisplayName) {
		value := plan.DisplayName.ValueString()
		displayName = &value
	}

	var linkedGroupUUID *string
	if !plan.LinkedGroup.Equal(state.LinkedGroup) {
		value, err := r.resolveGroupUUID(ctx, plan.LinkedGroup.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error Resolving Linked Group", err.Error())
			return
		}
		linkedGroupUUID = &value
	}

	if err := r.client.UpdateApplication(ctx, state.ID.ValueString(), displayName, linkedGroupUUID); err != nil {
		resp.Diagnostics.AddError("Error Updating Application", err.Error())
		return
	}

	app, err := r.client.GetApplication(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Application", err.Error())
		return
	}

	if err := r.syncState(ctx, &plan, app); err != nil {
		resp.Diagnostics.AddError("Error Reading Application", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteID := state.ID.ValueString()
	if deleteID == "" {
		deleteID = state.Name.ValueString()
	}
	if err := r.client.DeleteApplication(ctx, deleteID); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		resp.Diagnostics.AddError("Error Deleting Application", err.Error())
		return
	}
}

func (r *applicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
