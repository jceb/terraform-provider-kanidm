package client

import (
	"context"
	"fmt"
)

func (c *Client) GetGroupAttr(ctx context.Context, groupID, attr string) ([]string, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/v1/group/%s/_attr/%s", groupID, attr), nil)
	if err != nil {
		return nil, fmt.Errorf("get group attr %s: %w", attr, err)
	}

	var values []string
	if err := decodeResponse(resp, &values); err != nil {
		return nil, fmt.Errorf("decode group attr %s: %w", attr, err)
	}

	return values, nil
}

func (c *Client) SetGroupAttr(ctx context.Context, groupID, attr string, values []string) error {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/v1/group/%s/_attr/%s", groupID, attr), values)
	if err != nil {
		return fmt.Errorf("set group attr %s: %w", attr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *Client) DeleteGroupAttr(ctx context.Context, groupID, attr string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/group/%s/_attr/%s", groupID, attr), []string{})
	if err != nil {
		return fmt.Errorf("delete group attr %s: %w", attr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *Client) AddGroupClass(ctx context.Context, groupID, class string) error {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/group/%s/_attr/class", groupID), []string{class})
	if err != nil {
		return fmt.Errorf("add group class %s: %w", class, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *Client) RemoveGroupClass(ctx context.Context, groupID, class string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/group/%s/_attr/class", groupID), []string{class})
	if err != nil {
		return fmt.Errorf("remove group class %s: %w", class, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *Client) GetSystemAttr(ctx context.Context, attr string) ([]string, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/v1/system/_attr/%s", attr), nil)
	if err != nil {
		return nil, fmt.Errorf("get system attr %s: %w", attr, err)
	}

	var values []string
	if err := decodeResponse(resp, &values); err != nil {
		return nil, fmt.Errorf("decode system attr %s: %w", attr, err)
	}

	return values, nil
}

func (c *Client) AddSystemAttr(ctx context.Context, attr string, values []string) error {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/system/_attr/%s", attr), values)
	if err != nil {
		return fmt.Errorf("add system attr %s: %w", attr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *Client) RemoveSystemAttr(ctx context.Context, attr string, values []string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/system/_attr/%s", attr), values)
	if err != nil {
		return fmt.Errorf("remove system attr %s: %w", attr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *Client) SetSystemAttr(ctx context.Context, attr string, values []string) error {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/v1/system/_attr/%s", attr), values)
	if err != nil {
		return fmt.Errorf("set system attr %s: %w", attr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func (c *Client) DeleteSystemAttr(ctx context.Context, attr string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/system/_attr/%s", attr), []string{})
	if err != nil {
		return fmt.Errorf("delete system attr %s: %w", attr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}
