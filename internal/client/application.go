package client

import (
	"context"
	"fmt"
	"strings"
)

// Application represents a Kanidm application entry.
type Application struct {
	ID          string
	Name        string
	DisplayName string
	LinkedGroup string
}

type scimReference struct {
	UUID  *string `json:"uuid,omitempty"`
	Value *string `json:"value,omitempty"`
}

type scimReferenceResponse struct {
	UUID  string `json:"uuid"`
	Value string `json:"value"`
}

type scimApplicationCreateReq struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"displayname"`
	LinkedGroup scimReference `json:"linked_group"`
}

type scimApplicationEntry struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	DisplayName string                  `json:"displayname"`
	LinkedGroup []scimReferenceResponse `json:"linked_group"`
}

type scimEntryPutReq struct {
	ID          string           `json:"id"`
	DisplayName *string          `json:"displayname,omitempty"`
	LinkedGroup *[]scimReference `json:"linked_group,omitempty"`
}

func (c *Client) CreateApplication(ctx context.Context, name, displayName, linkedGroupID string) (*Application, error) {
	body := scimApplicationCreateReq{
		Name:        name,
		DisplayName: displayName,
		LinkedGroup: scimReference{UUID: &linkedGroupID},
	}

	resp, err := c.doRequest(ctx, "POST", "/scim/v1/Application", body)
	if err != nil {
		return nil, fmt.Errorf("create application: %w", err)
	}

	var entry scimApplicationEntry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, fmt.Errorf("create application: decode response: %w", err)
	}

	return applicationFromSCIM(&entry), nil
}

func (c *Client) GetApplication(ctx context.Context, id string) (*Application, error) {
	resp, err := c.doRequest(ctx, "GET", "/scim/v1/Application/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("get application: %w", err)
	}

	var entry scimApplicationEntry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, fmt.Errorf("get application: decode response: %w", err)
	}

	return applicationFromSCIM(&entry), nil
}

func (c *Client) UpdateApplication(ctx context.Context, id string, displayName, linkedGroupID *string) error {
	body := scimEntryPutReq{ID: id}
	if displayName != nil {
		body.DisplayName = displayName
	}
	if linkedGroupID != nil {
		refs := []scimReference{{UUID: linkedGroupID}}
		body.LinkedGroup = &refs
	}
	if body.DisplayName == nil && body.LinkedGroup == nil {
		return nil
	}

	resp, err := c.doRequest(ctx, "PUT", "/scim/v1/Entry", body)
	if err != nil {
		return fmt.Errorf("update application: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *Client) DeleteApplication(ctx context.Context, id string) error {
	resp, err := c.doRequest(ctx, "DELETE", "/scim/v1/Application/"+id, nil)
	if err != nil {
		return fmt.Errorf("delete application: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func applicationFromSCIM(entry *scimApplicationEntry) *Application {
	app := &Application{
		ID:          entry.ID,
		Name:        entry.Name,
		DisplayName: entry.DisplayName,
	}
	if len(entry.LinkedGroup) > 0 {
		if entry.LinkedGroup[0].UUID != "" {
			app.LinkedGroup = entry.LinkedGroup[0].UUID
		} else {
			app.LinkedGroup = stripSPNDomain(entry.LinkedGroup[0].Value)
		}
	}
	return app
}

func stripSPNDomain(value string) string {
	parts := strings.SplitN(value, "@", 2)
	return parts[0]
}
