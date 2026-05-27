package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/ssoriche/terraform-provider-kanidm/internal/client"
)

// Ensure the implementation satisfies the required interfaces
var (
	_ resource.Resource                = (*personResource)(nil)
	_ resource.ResourceWithImportState = (*personResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*personResource)(nil)
)

// NewPersonResource creates a new person resource
func NewPersonResource() resource.Resource {
	return &personResource{}
}

// personResource is the resource implementation
type personResource struct {
	client         *client.Client
	personDefaults personManagementDefaults
}

// personResourceModel describes the resource data model
type personResourceModel struct {
	Name                                types.String `tfsdk:"name"`
	ID                                  types.String `tfsdk:"id"`
	DisplayName                         types.String `tfsdk:"displayname"`
	NameManagement                      types.String `tfsdk:"name_management"`
	DisplayManagement                   types.String `tfsdk:"display_management"`
	Mail                                types.List   `tfsdk:"mail"`
	Password                            types.String `tfsdk:"password"`
	GenerateInitialCredentialResetToken types.Bool   `tfsdk:"generate_initial_credential_reset_token"`
	InitialCredentialResetToken         types.String `tfsdk:"initial_credential_reset_token"`
	InitialCredentialResetTokenTTL      types.Int64  `tfsdk:"initial_credential_reset_token_ttl"`
}

// Metadata returns the resource type name
func (r *personResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_person"
}

// Schema defines the schema for the resource
func (r *personResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm person account.

## Authentication Setup

Kanidm supports two credential setup workflows:

### Password-Based Authentication
Set the ` + "`password`" + ` attribute to create a password-based account:

` + "```hcl" + `
resource "kanidm_person" "example" {
  name        = "jdoe"
  displayname = "John Doe"
  password    = var.initial_password
}
` + "```" + `

### Passkey/Modern Authentication (Recommended)
Set ` + "`generate_initial_credential_reset_token = true`" + ` to generate a one-time onboarding token for credential setup via the Kanidm web UI:

` + "```hcl" + `
resource "kanidm_person" "example" {
  name                          = "jdoe"
  displayname                   = "John Doe"
  generate_initial_credential_reset_token = true
}

output "initial_credential_reset_token" {
  value     = kanidm_person.example.initial_credential_reset_token
  sensitive = true
}
` + "```" + `

The user can then visit the Kanidm web UI with the token to set up passkeys or passwords.`,

		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Person username. This can be managed continuously or treated as a create-time default.",
				Required:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Stable Kanidm UUID for this person. This value is computed after creation/import and used to keep the resource linked across external renames.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"displayname": schema.StringAttribute{
				MarkdownDescription: "Display name of the person.",
				Required:            true,
			},
			"name_management": schema.StringAttribute{
				MarkdownDescription: "How Terraform manages `name`. Valid values: `managed`, `initial`.",
				Optional:            true,
			},
			"display_management": schema.StringAttribute{
				MarkdownDescription: "How Terraform manages `displayname`. Valid values: `managed`, `initial`.",
				Optional:            true,
			},
			"mail": schema.ListAttribute{
				MarkdownDescription: "Email addresses for the person.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password for the person account. **Note:** This is write-only and will not be stored in state. " +
					"Mutually exclusive with `generate_initial_credential_reset_token`. " +
					"Consider using `lifecycle { ignore_changes = [password] }` if the password is managed externally.",
				Optional:  true,
				Sensitive: true,
			},
			"generate_initial_credential_reset_token": schema.BoolAttribute{
				MarkdownDescription: "Whether to generate a one-time initial credential reset token for onboarding via the web UI. " +
					"Mutually exclusive with `password`. Defaults to `false`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"initial_credential_reset_token": schema.StringAttribute{
				MarkdownDescription: "The initial credential reset token generated during resource creation when `generate_initial_credential_reset_token` is `true`. " +
					"This token can be used once to set up credentials via the Kanidm web UI. **Computed value only.**",
				Computed:  true,
				Sensitive: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"initial_credential_reset_token_ttl": schema.Int64Attribute{
				MarkdownDescription: "Time-to-live for the initial credential reset token in seconds. Defaults to 3600 (1 hour). Only used during resource creation.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(3600),
			},
		},
	}
}

// Configure adds the provider configured client to the resource
func (r *personResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data := configureResource(req, resp)
	if data == nil {
		return
	}

	r.client = data.client
	r.personDefaults = data.personDefaults
}

func (r *personResource) resolveManagementModes(plan personResourceModel) (string, string, bool, bool, error) {
	nameMode, diags := resolveManagementMode(plan.NameManagement, r.personDefaults.Name, "name_management")
	if diags.HasError() {
		return "", "", false, false, errors.New(diags[0].Summary())
	}

	displayMode, diags := resolveManagementMode(plan.DisplayManagement, r.personDefaults.Display, "display_management")
	if diags.HasError() {
		return "", "", false, false, errors.New(diags[0].Summary())
	}

	return nameMode, displayMode, nameMode == managementModeManaged, displayMode == managementModeManaged, nil
}

func (r *personResource) applyPersonState(ctx context.Context, model *personResourceModel, person *client.Person) error {
	model.ID = types.StringValue(person.UUID)
	model.Name = types.StringValue(person.Name)
	model.DisplayName = types.StringValue(person.DisplayName)

	if len(person.Mail) > 0 {
		mailList, diags := types.ListValueFrom(ctx, types.StringType, person.Mail)
		if diags.HasError() {
			return errors.New(diags.Errors()[0].Summary())
		}
		model.Mail = mailList
	} else {
		model.Mail = types.ListNull(types.StringType)
	}

	return nil
}

// ModifyPlan suppresses diffs for create-time-only attributes.
func (r *personResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var plan, state personResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	nameMode, diags := resolveManagementMode(plan.NameManagement, r.personDefaults.Name, "name_management")
	resp.Diagnostics.Append(diags...)
	displayMode, diags := resolveManagementMode(plan.DisplayManagement, r.personDefaults.Display, "display_management")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if nameMode == managementModeInitial && !state.Name.IsNull() && !state.Name.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("name"), state.Name)...)
	}

	if displayMode == managementModeInitial && !state.DisplayName.IsNull() && !state.DisplayName.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("displayname"), state.DisplayName)...)
	}

	if !state.GenerateInitialCredentialResetToken.IsNull() && !state.GenerateInitialCredentialResetToken.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("generate_initial_credential_reset_token"), state.GenerateInitialCredentialResetToken)...)
	}

	if !state.InitialCredentialResetTokenTTL.IsNull() && !state.InitialCredentialResetTokenTTL.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("initial_credential_reset_token_ttl"), state.InitialCredentialResetTokenTTL)...)
	}
}

// Create creates the resource and sets the initial Terraform state
func (r *personResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan personResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Validate mutually exclusive options
	hasPassword := !plan.Password.IsNull() && !plan.Password.IsUnknown()
	generateToken := r.personDefaults.GenerateInitialResetToken
	if !plan.GenerateInitialCredentialResetToken.IsNull() && !plan.GenerateInitialCredentialResetToken.IsUnknown() {
		generateToken = plan.GenerateInitialCredentialResetToken.ValueBool()
	}
	tokenTTL := r.personDefaults.InitialResetTokenTTLSeconds
	if !plan.InitialCredentialResetTokenTTL.IsNull() && !plan.InitialCredentialResetTokenTTL.IsUnknown() {
		tokenTTL = plan.InitialCredentialResetTokenTTL.ValueInt64()
	}

	if hasPassword && generateToken {
		resp.Diagnostics.AddError(
			"Conflicting Configuration",
			"Cannot specify both 'password' and 'generate_initial_credential_reset_token'. Choose one authentication setup method.",
		)
		return
	}

	if _, _, _, _, err := r.resolveManagementModes(plan); err != nil {
		resp.Diagnostics.AddError(
			"Invalid person management mode",
			err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "Creating person", map[string]any{
		"name": plan.Name.ValueString(),
	})

	// Create the person account
	person, err := r.client.CreatePerson(ctx, plan.Name.ValueString(), plan.DisplayName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Person",
			"Could not create person: "+err.Error(),
		)
		return
	}

	// Set password if provided
	if hasPassword {
		tflog.Debug(ctx, "Setting initial password for person")
		if err := r.client.SetPersonPassword(ctx, person.Name, plan.Password.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Password",
				"Person was created but password could not be set: "+err.Error(),
			)
			return
		}
	}

	// Generate credential reset token if requested
	if generateToken {
		tflog.Debug(ctx, "Generating credential reset token for person")
		ttl := int(tokenTTL)
		token, err := r.client.CreatePersonCredentialResetToken(ctx, person.Name, &ttl)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error Generating Credential Reset Token",
				"Person was created but credential reset token could not be generated: "+err.Error(),
			)
			return
		}
		plan.InitialCredentialResetToken = types.StringValue(token)
	}

	// Update mail if provided
	if !plan.Mail.IsNull() && !plan.Mail.IsUnknown() {
		var mailAddrs []string
		resp.Diagnostics.Append(plan.Mail.ElementsAs(ctx, &mailAddrs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		if len(mailAddrs) > 0 {
			tflog.Debug(ctx, "Updating mail addresses for person")
			if err := r.client.UpdatePerson(ctx, person.Name, nil, nil, mailAddrs); err != nil {
				resp.Diagnostics.AddError(
					"Error Updating Mail",
					"Person was created but mail addresses could not be set: "+err.Error(),
				)
				return
			}
		}
	}

	// Read back the person to get the current state
	createdPerson, err := r.client.GetPerson(ctx, person.Name)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Person was created but could not be read back: "+err.Error(),
		)
		return
	}

	if createdPerson.UUID == "" {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Person was created but the Kanidm response did not include a UUID.",
		)
		return
	}

	if err := r.applyPersonState(ctx, &plan, createdPerson); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Person was created but could not be mapped back to Terraform state: "+err.Error(),
		)
		return
	}

	// Password is write-only, keep the planned value but don't try to read it back

	// Ensure initial credential reset token fields are properly set with defaults if not already set.
	if plan.GenerateInitialCredentialResetToken.IsNull() || plan.GenerateInitialCredentialResetToken.IsUnknown() {
		plan.GenerateInitialCredentialResetToken = types.BoolValue(generateToken)
	}
	if plan.InitialCredentialResetTokenTTL.IsNull() || plan.InitialCredentialResetTokenTTL.IsUnknown() {
		plan.InitialCredentialResetTokenTTL = types.Int64Value(tokenTTL)
	}
	// If the initial token wasn't generated, ensure it's null not unknown.
	if plan.InitialCredentialResetToken.IsUnknown() {
		plan.InitialCredentialResetToken = types.StringNull()
	}

	tflog.Debug(ctx, "Person created successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the Terraform state with the latest data
func (r *personResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state personResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading person", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Get current person from API
	person, err := r.client.GetPerson(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "Person not found, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Could not read person: "+err.Error(),
		)
		return
	}

	if person.UUID == "" {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Kanidm did not return a UUID for the requested person.",
		)
		return
	}

	if err := r.applyPersonState(ctx, &state, person); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Could not map person data into Terraform state: "+err.Error(),
		)
		return
	}

	// Password is write-only and not readable from API, preserve existing state value
	// Initial credential reset token fields should use defaults when not explicitly set.
	if state.GenerateInitialCredentialResetToken.IsNull() || state.GenerateInitialCredentialResetToken.IsUnknown() {
		state.GenerateInitialCredentialResetToken = types.BoolValue(r.personDefaults.GenerateInitialResetToken)
	}
	if state.InitialCredentialResetTokenTTL.IsNull() || state.InitialCredentialResetTokenTTL.IsUnknown() {
		state.InitialCredentialResetTokenTTL = types.Int64Value(r.personDefaults.InitialResetTokenTTLSeconds)
	}
	// The initial reset token is only set during Create when generated, otherwise null.
	if state.InitialCredentialResetToken.IsUnknown() {
		state.InitialCredentialResetToken = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update updates the resource and sets the updated Terraform state
func (r *personResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state personResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Updating person", map[string]any{
		"id": state.ID.ValueString(),
	})

	_, _, manageName, manageDisplay, err := r.resolveManagementModes(plan)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid person management mode",
			err.Error(),
		)
		return
	}

	// Prepare mail addresses
	var mailAddrs []string
	updateMail := !plan.Mail.IsNull() && !plan.Mail.IsUnknown()
	if !plan.Mail.IsNull() && !plan.Mail.IsUnknown() {
		resp.Diagnostics.Append(plan.Mail.ElementsAs(ctx, &mailAddrs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	var nameValue *string
	if manageName {
		name := plan.Name.ValueString()
		nameValue = &name
	}

	var displayValue *string
	if manageDisplay {
		display := plan.DisplayName.ValueString()
		displayValue = &display
	}

	if nameValue != nil || displayValue != nil || updateMail {
		// Update person attributes using the stable UUID-backed resource ID.
		if err := r.client.UpdatePerson(ctx, state.ID.ValueString(), nameValue, displayValue, mailAddrs); err != nil {
			resp.Diagnostics.AddError(
				"Error Updating Person",
				"Could not update person: "+err.Error(),
			)
			return
		}
	}

	// Update password if changed
	if !plan.Password.Equal(state.Password) && !plan.Password.IsNull() {
		tflog.Debug(ctx, "Updating password for person")
		if err := r.client.SetPersonPassword(ctx, state.ID.ValueString(), plan.Password.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error Updating Password",
				"Person was updated but password could not be changed: "+err.Error(),
			)
			return
		}
	}

	// Read back the updated person
	updatedPerson, err := r.client.GetPerson(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Person was updated but could not be read back: "+err.Error(),
		)
		return
	}

	if updatedPerson.UUID == "" {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Person was updated but the Kanidm response did not include a UUID.",
		)
		return
	}

	if err := r.applyPersonState(ctx, &plan, updatedPerson); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Person was updated but could not be mapped back to Terraform state: "+err.Error(),
		)
		return
	}

	// Ensure initial credential reset token fields are properly set.
	if plan.GenerateInitialCredentialResetToken.IsNull() || plan.GenerateInitialCredentialResetToken.IsUnknown() {
		plan.GenerateInitialCredentialResetToken = state.GenerateInitialCredentialResetToken
	}
	if plan.InitialCredentialResetTokenTTL.IsNull() || plan.InitialCredentialResetTokenTTL.IsUnknown() {
		plan.InitialCredentialResetTokenTTL = state.InitialCredentialResetTokenTTL
	}
	plan.InitialCredentialResetToken = state.InitialCredentialResetToken
	if plan.InitialCredentialResetToken.IsUnknown() {
		plan.InitialCredentialResetToken = types.StringNull()
	}

	tflog.Debug(ctx, "Person updated successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete deletes the resource and removes the Terraform state
func (r *personResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state personResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Deleting person", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Delete the person
	if err := r.client.DeletePerson(ctx, state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			// Person already deleted, just remove from state
			tflog.Warn(ctx, "Person not found during delete, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting Person",
			"Could not delete person: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "Person deleted successfully", map[string]any{
		"id": state.ID.ValueString(),
	})
}

// ImportState imports an existing person into Terraform state
func (r *personResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Use the ID (username) directly as the import identifier
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)

	tflog.Debug(ctx, "Imported person", map[string]any{
		"id": req.ID,
	})
}
