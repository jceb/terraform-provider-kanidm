package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/ssoriche/terraform-provider-kanidm/internal/client"
)

var (
	_ resource.Resource = (*groupMembersResource)(nil)
)

func NewGroupMembersResource() resource.Resource {
	return &groupMembersResource{}
}

type groupMembersResource struct {
	client *client.Client
}

type groupMembersResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Group   types.String `tfsdk:"group"`
	Members types.Set    `tfsdk:"members"`
}

func (r *groupMembersResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_members"
}

func (r *groupMembersResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a subset of group memberships without taking ownership of the group's full member list.

Use this for built-in or externally managed groups where Terraform should only add/remove the listed members.

Do not use this resource against the same target group as ` + "`kanidm_group.members`" + `; that overlap is unsupported.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Stable Kanidm UUID for the target group.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"group": schema.StringAttribute{
				MarkdownDescription: "Target group identifier. May be a built-in group name, SPN, UUID, or a referenced `kanidm_group` ID.",
				Required:            true,
			},
			"members": schema.SetAttribute{
				MarkdownDescription: "Set of members this resource should ensure are present in the target group. Only this managed subset is added/removed.",
				Required:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (r *groupMembersResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	data := configureResource(req, resp)
	if data == nil {
		return
	}
	r.client = data.client
}

func (r *groupMembersResource) resolveGroupUUID(ctx context.Context, identifier string) (string, error) {
	group, err := r.client.GetGroup(ctx, identifier)
	if err != nil {
		return "", err
	}
	if group.UUID == "" {
		return "", errors.New("group did not return a UUID")
	}
	return group.UUID, nil
}

func (r *groupMembersResource) normalizeMemberIdentifiers(ctx context.Context, members []string) ([]string, error) {
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

func (r *groupMembersResource) readRawMembers(ctx context.Context, members types.Set) ([]string, error) {
	if members.IsNull() || members.IsUnknown() {
		return []string{}, nil
	}
	var raw []string
	diags := members.ElementsAs(ctx, &raw, false)
	if diags.HasError() {
		return nil, errors.New(diags.Errors()[0].Summary())
	}
	return raw, nil
}

func toStringSet(ctx context.Context, values []string) (types.Set, error) {
	if len(values) == 0 {
		return types.SetNull(types.StringType), nil
	}
	setValue, diags := types.SetValueFrom(ctx, types.StringType, values)
	if diags.HasError() {
		return types.SetNull(types.StringType), errors.New(diags.Errors()[0].Summary())
	}
	return setValue, nil
}

func stringSetDiff(left, right []string) []string {
	if len(left) == 0 {
		return []string{}
	}
	rightMap := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightMap[value] = struct{}{}
	}
	diff := make([]string, 0, len(left))
	for _, value := range left {
		if _, exists := rightMap[value]; !exists {
			diff = append(diff, value)
		}
	}
	return diff
}

func (r *groupMembersResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupMembersResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	groupUUID, err := r.resolveGroupUUID(ctx, plan.Group.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving Group", err.Error())
		return
	}
	desiredRawMembers, err := r.readRawMembers(ctx, plan.Members)
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving Members", err.Error())
		return
	}
	desiredMembers, err := r.normalizeMemberIdentifiers(ctx, desiredRawMembers)
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving Members", err.Error())
		return
	}
	group, err := r.client.GetGroup(ctx, groupUUID)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Group", err.Error())
		return
	}
	currentMembers, err := r.normalizeMemberIdentifiers(ctx, group.Members)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Group Members", err.Error())
		return
	}
	toAdd := stringSetDiff(desiredMembers, currentMembers)
	if len(toAdd) > 0 {
		if err := r.client.AddGroupMembers(ctx, groupUUID, toAdd); err != nil {
			resp.Diagnostics.AddError("Error Adding Group Members", err.Error())
			return
		}
	}
	plan.ID = types.StringValue(groupUUID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupMembersResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupMembersResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err := r.client.GetGroup(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Group", err.Error())
		return
	}
	trackedRawMembers, err := r.readRawMembers(ctx, state.Members)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Managed Members", err.Error())
		return
	}
	trackedMembers, err := r.normalizeMemberIdentifiers(ctx, trackedRawMembers)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Managed Members", err.Error())
		return
	}
	currentMembers, err := r.normalizeMemberIdentifiers(ctx, group.Members)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Group Members", err.Error())
		return
	}
	currentMemberMap := make(map[string]struct{}, len(currentMembers))
	for _, member := range currentMembers {
		currentMemberMap[member] = struct{}{}
	}
	managedRawMembers := make([]string, 0, len(trackedRawMembers))
	for i, member := range trackedMembers {
		if _, exists := currentMemberMap[member]; exists {
			managedRawMembers = append(managedRawMembers, trackedRawMembers[i])
		}
	}
	state.ID = types.StringValue(group.UUID)
	state.Members, err = toStringSet(ctx, managedRawMembers)
	if err != nil {
		resp.Diagnostics.AddError("Error Setting State", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupMembersResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupMembersResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	oldGroupUUID := state.ID.ValueString()
	newGroupUUID, err := r.resolveGroupUUID(ctx, plan.Group.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving Group", err.Error())
		return
	}
	previousRawMembers, err := r.readRawMembers(ctx, state.Members)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Managed Members", err.Error())
		return
	}
	previousMembers, err := r.normalizeMemberIdentifiers(ctx, previousRawMembers)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Managed Members", err.Error())
		return
	}
	desiredRawMembers, err := r.readRawMembers(ctx, plan.Members)
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving Members", err.Error())
		return
	}
	desiredMembers, err := r.normalizeMemberIdentifiers(ctx, desiredRawMembers)
	if err != nil {
		resp.Diagnostics.AddError("Error Resolving Members", err.Error())
		return
	}
	if oldGroupUUID != newGroupUUID {
		if len(previousMembers) > 0 {
			if err := r.client.RemoveGroupMembers(ctx, oldGroupUUID, previousMembers); err != nil {
				resp.Diagnostics.AddError("Error Removing Group Members", err.Error())
				return
			}
		}
		if len(desiredMembers) > 0 {
			if err := r.client.AddGroupMembers(ctx, newGroupUUID, desiredMembers); err != nil {
				resp.Diagnostics.AddError("Error Adding Group Members", err.Error())
				return
			}
		}
	} else {
		toAdd := stringSetDiff(desiredMembers, previousMembers)
		toRemove := stringSetDiff(previousMembers, desiredMembers)
		if len(toAdd) > 0 {
			if err := r.client.AddGroupMembers(ctx, newGroupUUID, toAdd); err != nil {
				resp.Diagnostics.AddError("Error Adding Group Members", err.Error())
				return
			}
		}
		if len(toRemove) > 0 {
			if err := r.client.RemoveGroupMembers(ctx, newGroupUUID, toRemove); err != nil {
				resp.Diagnostics.AddError("Error Removing Group Members", err.Error())
				return
			}
		}
	}
	plan.ID = types.StringValue(newGroupUUID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupMembersResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupMembersResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	trackedRawMembers, err := r.readRawMembers(ctx, state.Members)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Managed Members", err.Error())
		return
	}
	trackedMembers, err := r.normalizeMemberIdentifiers(ctx, trackedRawMembers)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Managed Members", err.Error())
		return
	}
	if len(trackedMembers) == 0 {
		return
	}
	if err := r.client.RemoveGroupMembers(ctx, state.ID.ValueString(), trackedMembers); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error Removing Group Members", err.Error())
		return
	}
	tflog.Debug(ctx, "Removed managed group membership subset", map[string]any{"group_id": state.ID.ValueString()})
}
