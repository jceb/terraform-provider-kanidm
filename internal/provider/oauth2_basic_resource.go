package provider

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"

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
	_ resource.Resource                = (*oauth2BasicResource)(nil)
	_ resource.ResourceWithImportState = (*oauth2BasicResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*oauth2BasicResource)(nil)
)

func NewOAuth2BasicResource() resource.Resource {
	return &oauth2BasicResource{}
}

type oauth2BasicResource struct {
	client *client.Client
}

type oauth2BasicResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	DisplayName  types.String `tfsdk:"displayname"`
	Origin       types.String `tfsdk:"origin"`
	RedirectURIs types.List   `tfsdk:"redirect_uris"`
	ImagePath    types.String `tfsdk:"image_path"`
	ImageSHA256  types.String `tfsdk:"image_sha256"`
	ScopeMaps    types.Set    `tfsdk:"scope_map"`
	ClientSecret types.String `tfsdk:"client_secret"`
}

type scopeMapModel struct {
	Group  types.String `tfsdk:"group"`
	Scopes types.List   `tfsdk:"scopes"`
}

func firstKnownString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""
}

func hashFileSHA256(filePath string) (string, error) {
	contents, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file %q: %w", filePath, err)
	}

	sum := sha256.Sum256(contents)
	return fmt.Sprintf("%x", sum[:]), nil
}

func (r *oauth2BasicResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_oauth2_basic"
}

func (r *oauth2BasicResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm OAuth2 basic (confidential) client.

OAuth2 basic clients are used for server-side applications that can securely store a client secret.
The client secret is automatically generated on creation and can be used for OAuth2/OIDC authentication.

## Example Usage

` + "```hcl" + `
resource "kanidm_oauth2_basic" "grafana" {
  name        = "grafana"
  displayname = "Grafana"
  origin      = "https://grafana.example.com"

  redirect_uris = [
    "https://grafana.example.com/login/generic_oauth"
  ]

  scope_map {
    group  = "admins"
    scopes = ["openid", "profile", "email", "groups"]
  }

  scope_map {
    group  = "developers"
    scopes = ["openid", "profile", "email"]
  }
}

# Store the client secret in 1Password or another secret manager
output "grafana_client_secret" {
  value     = kanidm_oauth2_basic.grafana.client_secret
  sensitive = true
}
` + "```" + `

**Important:** The client secret is only available during creation and cannot be recovered later.
Store it securely immediately after creation. You can regenerate it using the Kanidm CLI if needed.`,

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Stable Kanidm UUID for this OAuth2 client. This value is computed after creation/import and used to keep the resource linked across external renames.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "OAuth2 client name (client ID). This can be changed outside Terraform, but is tracked via the stable UUID in `id`.",
				Required:            true,
			},
			"displayname": schema.StringAttribute{
				MarkdownDescription: "Display name of the OAuth2 client.",
				Required:            true,
			},
			"origin": schema.StringAttribute{
				MarkdownDescription: "Origin URL where the OAuth2 client application is hosted (e.g., https://grafana.example.com).",
				Required:            true,
			},
			"redirect_uris": schema.ListAttribute{
				MarkdownDescription: "List of allowed redirect URIs for OAuth2 callbacks.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"image_path": schema.StringAttribute{
				MarkdownDescription: "Optional local path to an image file to upload for the OAuth2 client. The provider tracks the local file hash, but does not currently detect remote image changes made outside Terraform.",
				Optional:            true,
			},
			"image_sha256": schema.StringAttribute{
				MarkdownDescription: "SHA-256 hash of the local image file last applied for this OAuth2 client.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "Client secret for the OAuth2 basic client. **Only available during creation.** " +
					"Store this secret securely as it cannot be retrieved later.",
				Computed:  true,
				Sensitive: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
		Blocks: map[string]schema.Block{
			"scope_map": schema.SetNestedBlock{
				MarkdownDescription: "Scope mappings that define which OAuth2 scopes are granted to members of specific groups. " +
					"Each scope_map block links a Kanidm group to a set of OAuth2 scopes.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"group": schema.StringAttribute{
							MarkdownDescription: "Name of the Kanidm group to map scopes to.",
							Required:            true,
						},
						"scopes": schema.ListAttribute{
							MarkdownDescription: "List of OAuth2 scopes to grant to group members (e.g., openid, profile, email, groups).",
							Required:            true,
							ElementType:         types.StringType,
						},
					},
				},
			},
		},
	}
}

func (r *oauth2BasicResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data := configureResource(req, resp)
	if data == nil {
		return
	}

	r.client = data.client
}

func (r *oauth2BasicResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan oauth2BasicResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.ImagePath.IsNull() || plan.ImagePath.IsUnknown() || plan.ImagePath.ValueString() == "" {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("image_sha256"), types.StringNull())...)
		return
	}

	hash, err := hashFileSHA256(plan.ImagePath.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Hashing OAuth2 Image", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("image_sha256"), types.StringValue(hash))...)
}

func (r *oauth2BasicResource) applyOAuth2BasicState(ctx context.Context, model *oauth2BasicResourceModel, oauth2Client *client.OAuth2Client) error {
	if oauth2Client.UUID == "" {
		return errors.New("Kanidm did not return a UUID for the requested OAuth2 client")
	}

	model.ID = types.StringValue(oauth2Client.UUID)
	model.Name = types.StringValue(oauth2Client.Name)
	model.DisplayName = types.StringValue(oauth2Client.DisplayName)
	model.Origin = types.StringValue(oauth2Client.Origin)

	if len(oauth2Client.RedirectURIs) > 0 {
		redirectURIsList, diags := types.ListValueFrom(ctx, types.StringType, oauth2Client.RedirectURIs)
		if diags.HasError() {
			return errors.New(diags.Errors()[0].Summary())
		}
		model.RedirectURIs = redirectURIsList
	} else {
		model.RedirectURIs = types.ListNull(types.StringType)
	}

	if model.ImagePath.IsNull() || model.ImagePath.IsUnknown() || model.ImagePath.ValueString() == "" {
		model.ImageSHA256 = types.StringNull()
	}

	return nil
}

func (r *oauth2BasicResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan oauth2BasicResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Creating OAuth2 basic client", map[string]any{
		"name": plan.Name.ValueString(),
	})

	// Create the OAuth2 basic client (this generates the client secret)
	oauth2Client, err := r.client.CreateOAuth2BasicClient(
		ctx,
		plan.Name.ValueString(),
		plan.DisplayName.ValueString(),
		plan.Origin.ValueString(),
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating OAuth2 Basic Client",
			"Could not create OAuth2 basic client: "+err.Error(),
		)
		return
	}

	// Update origin and redirect URIs after creation
	var redirectURIs []string
	if !plan.RedirectURIs.IsNull() && !plan.RedirectURIs.IsUnknown() {
		resp.Diagnostics.Append(plan.RedirectURIs.ElementsAs(ctx, &redirectURIs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	tflog.Debug(ctx, "Setting displayname, origin and redirect URIs for OAuth2 client", map[string]any{
		"displayname":    plan.DisplayName.ValueString(),
		"origin":         plan.Origin.ValueString(),
		"redirect_count": len(redirectURIs),
	})

	if err := r.client.UpdateOAuth2Client(ctx, oauth2Client.Name, nil, plan.DisplayName.ValueString(), plan.Origin.ValueString(), redirectURIs); err != nil {
		resp.Diagnostics.AddError(
			"Error Setting OAuth2 Configuration",
			"OAuth2 client was created but configuration could not be set: "+err.Error(),
		)
		return
	}

	// Configure scope maps if provided
	if !plan.ScopeMaps.IsNull() && !plan.ScopeMaps.IsUnknown() {
		var scopeMaps []scopeMapModel
		resp.Diagnostics.Append(plan.ScopeMaps.ElementsAs(ctx, &scopeMaps, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		for _, scopeMap := range scopeMaps {
			var scopes []string
			resp.Diagnostics.Append(scopeMap.Scopes.ElementsAs(ctx, &scopes, false)...)
			if resp.Diagnostics.HasError() {
				return
			}

			tflog.Debug(ctx, "Setting scope map for OAuth2 client", map[string]any{
				"group":  scopeMap.Group.ValueString(),
				"scopes": scopes,
			})

			if err := r.client.SetOAuth2ScopeMap(ctx, oauth2Client.Name, scopeMap.Group.ValueString(), scopes); err != nil {
				resp.Diagnostics.AddError(
					"Error Setting Scope Map",
					"OAuth2 client was created but scope map could not be configured: "+err.Error(),
				)
				return
			}
		}
	}

	if !plan.ImagePath.IsNull() && !plan.ImagePath.IsUnknown() && plan.ImagePath.ValueString() != "" {
		if err := r.client.UploadOAuth2Image(ctx, oauth2Client.Name, plan.ImagePath.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error Uploading OAuth2 Image",
				"OAuth2 client was created but the image could not be uploaded: "+err.Error(),
			)
			return
		}
	}

	// Read back the created OAuth2 client
	createdClient, err := r.client.ResolveOAuth2Client(ctx, oauth2Client.Name)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Client",
			"OAuth2 client was created but could not be read back: "+err.Error(),
		)
		return
	}

	if err := r.applyOAuth2BasicState(ctx, &plan, createdClient); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Client",
			"OAuth2 client was created but could not be mapped back to Terraform state: "+err.Error(),
		)
		return
	}
	plan.ClientSecret = types.StringValue(oauth2Client.ClientSecret)
	if plan.ImagePath.IsNull() || plan.ImagePath.IsUnknown() || plan.ImagePath.ValueString() == "" {
		plan.ImageSHA256 = types.StringNull()
	}

	// Keep the scope maps from the plan (can't read them back from API in current form)
	// In a future enhancement, we could parse the scope maps from the API response

	tflog.Debug(ctx, "OAuth2 basic client created successfully", map[string]any{
		"name": plan.Name.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *oauth2BasicResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state oauth2BasicResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading OAuth2 basic client", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Get current OAuth2 client from API.
	oauth2Client, err := r.client.ResolveOAuth2Client(ctx, firstKnownString(state.ID.ValueString(), state.Name.ValueString()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "OAuth2 basic client not found, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Basic Client",
			"Could not read OAuth2 basic client: "+err.Error(),
		)
		return
	}

	// Verify this is a basic (confidential) client
	if oauth2Client.IsPublic {
		resp.Diagnostics.AddError(
			"Invalid Client Type",
			"Expected OAuth2 basic (confidential) client but found public client. "+
				"This resource manages basic clients only.",
		)
		return
	}

	if err := r.applyOAuth2BasicState(ctx, &state, oauth2Client); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Basic Client",
			"Could not map OAuth2 client data into Terraform state: "+err.Error(),
		)
		return
	}

	// Retrieve client secret if not already in state (e.g., after import)
	if state.ClientSecret.IsNull() || state.ClientSecret.ValueString() == "" {
		tflog.Debug(ctx, "Client secret not in state, retrieving from API", map[string]any{
			"name": state.Name.ValueString(),
		})
		secret, err := r.client.GetOAuth2BasicSecret(ctx, oauth2Client.Name)
		if err != nil {
			tflog.Warn(ctx, "Could not retrieve client secret", map[string]any{
				"name":  oauth2Client.Name,
				"error": err.Error(),
			})
			// Don't fail the read, just leave secret empty
		} else {
			state.ClientSecret = types.StringValue(secret)
			tflog.Debug(ctx, "Retrieved client secret successfully", map[string]any{
				"name": oauth2Client.Name,
			})
		}
	}

	// Scope maps preserved from state (can't read them back in current form)
	// Local image tracking is preserved from state/config only.

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *oauth2BasicResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state oauth2BasicResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Updating OAuth2 basic client", map[string]any{
		"id": state.ID.ValueString(),
	})

	resolvedClient, err := r.client.ResolveOAuth2Client(ctx, firstKnownString(state.ID.ValueString(), state.Name.ValueString()))
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Resolving OAuth2 Basic Client",
			"Could not resolve OAuth2 basic client before update: "+err.Error(),
		)
		return
	}

	// Prepare redirect URIs
	var redirectURIs []string
	if !plan.RedirectURIs.IsNull() && !plan.RedirectURIs.IsUnknown() {
		resp.Diagnostics.Append(plan.RedirectURIs.ElementsAs(ctx, &redirectURIs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Detect name changes so we can rename the OAuth2 client if needed.
	nameChanged := !plan.Name.Equal(state.Name)
	var newName *string
	if nameChanged {
		n := plan.Name.ValueString()
		newName = &n
	}

	// Update OAuth2 client (name, displayname, origin, redirect URIs)
	if err := r.client.UpdateOAuth2Client(
		ctx,
		resolvedClient.Name,
		newName,
		plan.DisplayName.ValueString(),
		plan.Origin.ValueString(),
		redirectURIs,
	); err != nil {
		resp.Diagnostics.AddError(
			"Error Updating OAuth2 Basic Client",
			"Could not update OAuth2 basic client: "+err.Error(),
		)
		return
	}

	// After a successful rename, subsequent API calls must use the new name.
	effectiveName := resolvedClient.Name
	if nameChanged {
		effectiveName = plan.Name.ValueString()
	}

	// Handle scope map changes
	// Get old and new scope maps
	var oldScopeMaps, newScopeMaps []scopeMapModel
	resp.Diagnostics.Append(state.ScopeMaps.ElementsAs(ctx, &oldScopeMaps, false)...)
	resp.Diagnostics.Append(plan.ScopeMaps.ElementsAs(ctx, &newScopeMaps, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build maps for easier comparison
	oldScopeMapsByGroup := make(map[string][]string)
	for _, sm := range oldScopeMaps {
		var scopes []string
		resp.Diagnostics.Append(sm.Scopes.ElementsAs(ctx, &scopes, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		oldScopeMapsByGroup[sm.Group.ValueString()] = scopes
	}

	newScopeMapsByGroup := make(map[string][]string)
	for _, sm := range newScopeMaps {
		var scopes []string
		resp.Diagnostics.Append(sm.Scopes.ElementsAs(ctx, &scopes, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		newScopeMapsByGroup[sm.Group.ValueString()] = scopes
	}

	// Delete scope maps that are no longer present
	for group := range oldScopeMapsByGroup {
		if _, exists := newScopeMapsByGroup[group]; !exists {
			tflog.Debug(ctx, "Deleting scope map", map[string]any{
				"group": group,
			})
			if err := r.client.DeleteOAuth2ScopeMap(ctx, effectiveName, group); err != nil {
				resp.Diagnostics.AddError(
					"Error Deleting Scope Map",
					"Could not delete scope map: "+err.Error(),
				)
				return
			}
		}
	}

	// Add or update scope maps
	for group, scopes := range newScopeMapsByGroup {
		tflog.Debug(ctx, "Setting scope map", map[string]any{
			"group":  group,
			"scopes": scopes,
		})
		if err := r.client.SetOAuth2ScopeMap(ctx, effectiveName, group, scopes); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Scope Map",
				"Could not set scope map: "+err.Error(),
			)
			return
		}
	}

	if plan.ImagePath.IsNull() || plan.ImagePath.IsUnknown() || plan.ImagePath.ValueString() == "" {
		if !state.ImageSHA256.IsNull() && !state.ImageSHA256.IsUnknown() && state.ImageSHA256.ValueString() != "" {
			if err := r.client.DeleteOAuth2Image(ctx, effectiveName); err != nil {
				resp.Diagnostics.AddError(
					"Error Deleting OAuth2 Image",
					"Could not delete OAuth2 image: "+err.Error(),
				)
				return
			}
		}
	} else if !plan.ImageSHA256.Equal(state.ImageSHA256) {
		if err := r.client.UploadOAuth2Image(ctx, effectiveName, plan.ImagePath.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error Uploading OAuth2 Image",
				"Could not upload OAuth2 image: "+err.Error(),
			)
			return
		}
	}

	// Read back the updated OAuth2 client
	updatedClient, err := r.client.ResolveOAuth2Client(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Client",
			"OAuth2 client was updated but could not be read back: "+err.Error(),
		)
		return
	}

	if err := r.applyOAuth2BasicState(ctx, &plan, updatedClient); err != nil {
		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Client",
			"OAuth2 client was updated but could not be mapped back to Terraform state: "+err.Error(),
		)
		return
	}

	// Preserve client secret from state (cannot be read back from API)
	plan.ClientSecret = state.ClientSecret
	if plan.ImagePath.IsNull() || plan.ImagePath.IsUnknown() || plan.ImagePath.ValueString() == "" {
		plan.ImageSHA256 = types.StringNull()
	}

	tflog.Debug(ctx, "OAuth2 basic client updated successfully", map[string]any{
		"name": plan.Name.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *oauth2BasicResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state oauth2BasicResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Deleting OAuth2 basic client", map[string]any{
		"id": state.ID.ValueString(),
	})

	resolvedClient, err := r.client.ResolveOAuth2Client(ctx, firstKnownString(state.ID.ValueString(), state.Name.ValueString()))
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return
		}

		resp.Diagnostics.AddError(
			"Error Resolving OAuth2 Basic Client",
			"Could not resolve OAuth2 basic client before delete: "+err.Error(),
		)
		return
	}

	// Delete the OAuth2 client
	if err := r.client.DeleteOAuth2Client(ctx, resolvedClient.Name); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "OAuth2 basic client not found during delete, removing from state", map[string]any{
				"name": state.Name.ValueString(),
			})
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting OAuth2 Basic Client",
			"Could not delete OAuth2 basic client: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "OAuth2 basic client deleted successfully", map[string]any{
		"name": state.Name.ValueString(),
	})
}

func (r *oauth2BasicResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Use the name directly as the import identifier
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)

	tflog.Debug(ctx, "Imported OAuth2 basic client", map[string]any{
		"name": req.ID,
	})

	// Add a warning about the client secret
	resp.Diagnostics.AddWarning(
		"Client Secret Not Available",
		"The client secret for this OAuth2 basic client is not available after import. "+
			"If you need the secret, you must regenerate it manually using the Kanidm CLI (kanidm system oauth2 basic_secret_read).",
	)
}
