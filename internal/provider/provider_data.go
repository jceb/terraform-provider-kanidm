package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ssoriche/terraform-provider-kanidm/internal/client"
)

const (
	managementModeManaged = "managed"
	managementModeInitial = "initial"
)

type personDefaultsModel struct {
	NameManagement                      types.String `tfsdk:"name_management"`
	DisplayManagement                   types.String `tfsdk:"display_management"`
	LegalNameManagement                 types.String `tfsdk:"legalname_management"`
	GenerateInitialCredentialResetToken types.Bool   `tfsdk:"generate_initial_credential_reset_token"`
	InitialCredentialResetTokenTTL      types.Int64  `tfsdk:"initial_credential_reset_token_ttl"`
}

type providerData struct {
	client         *client.Client
	personDefaults personManagementDefaults
}

type personManagementDefaults struct {
	Name                        string
	Display                     string
	LegalName                   string
	GenerateInitialResetToken   bool
	InitialResetTokenTTLSeconds int64
}

func configureResource(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *providerData {
	if req.ProviderData == nil {
		return nil
	}

	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			"Expected *providerData. Please report this issue to the provider developers.",
		)
		return nil
	}

	return data
}

func resolveManagementMode(value types.String, fallback string, attrName string) (string, diag.Diagnostics) {
	var diags diag.Diagnostics

	if value.IsNull() || value.IsUnknown() || value.ValueString() == "" {
		return fallback, diags
	}

	switch value.ValueString() {
	case managementModeManaged, managementModeInitial:
		return value.ValueString(), diags
	default:
		diags.AddError(
			"Invalid management mode",
			"Attribute '"+attrName+"' must be either 'managed' or 'initial'.",
		)
		return "", diags
	}
}
