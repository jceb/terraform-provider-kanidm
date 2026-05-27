package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
)

// OAuth2Client represents a Kanidm OAuth2 resource server
type OAuth2Client struct {
	UUID         string
	Name         string
	DisplayName  string
	Origin       string
	RedirectURIs []string
	ScopeMaps    map[string][]string
	ClientID     string // Computed
	ClientSecret string // Only for basic/confidential clients, populated on creation
	IsPublic     bool
}

// ListOAuth2Clients lists all OAuth2 clients visible to the caller.
func (c *Client) ListOAuth2Clients(ctx context.Context) ([]OAuth2Client, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/oauth2", nil)
	if err != nil {
		return nil, fmt.Errorf("list oauth2 clients: %w", err)
	}

	var entries []Entry
	if err := decodeResponse(resp, &entries); err != nil {
		return nil, err
	}

	clients := make([]OAuth2Client, 0, len(entries))
	for _, entry := range entries {
		_, hasBasicSecret := entry.Attrs["oauth2_rs_basic_secret"]
		isPublic := !hasBasicSecret

		clientName := entry.GetString("name")
		if clientName == "" {
			clientName = entry.GetString("oauth2_rs_name")
		}

		origin := entry.GetString("oauth2_rs_origin")
		if len(origin) > 0 && origin[len(origin)-1] == '/' {
			origin = origin[:len(origin)-1]
		}

		clients = append(clients, OAuth2Client{
			UUID:         firstNonEmpty(entry.GetString("entryuuid"), entry.GetString("uuid")),
			Name:         clientName,
			DisplayName:  entry.GetString("displayname"),
			Origin:       origin,
			RedirectURIs: entry.GetStringSlice("oauth2_rs_origin_landing"),
			ClientID:     clientName,
			IsPublic:     isPublic,
		})
	}

	return clients, nil
}

// ResolveOAuth2Client resolves an OAuth2 client by current name or stable UUID.
func (c *Client) ResolveOAuth2Client(ctx context.Context, identifier string) (*OAuth2Client, error) {
	clients, err := c.ListOAuth2Clients(ctx)
	if err != nil {
		return nil, err
	}

	for _, oauth2Client := range clients {
		if oauth2Client.UUID == identifier || oauth2Client.Name == identifier {
			clientCopy := oauth2Client
			return &clientCopy, nil
		}
	}

	return nil, ErrNotFound
}

// CreateOAuth2BasicClient creates a new OAuth2 basic (confidential) client
func (c *Client) CreateOAuth2BasicClient(ctx context.Context, name, displayName, origin string) (*OAuth2Client, error) {
	req := NewCreateRequest(map[string]any{
		"name":                     []string{name},
		"displayname":              []string{displayName},
		"oauth2_rs_origin_landing": []string{origin},
	})

	resp, err := c.doRequest(ctx, "POST", "/v1/oauth2/_basic", req)
	if err != nil {
		return nil, fmt.Errorf("create oauth2 basic client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// The create response doesn't include the client secret
	// We need to retrieve it using the show secret endpoint
	clientSecret, err := c.GetOAuth2BasicSecret(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("retrieve client secret: %w", err)
	}

	return &OAuth2Client{
		Name:         name,
		DisplayName:  displayName,
		Origin:       origin,
		ClientID:     name, // Client ID is typically the name
		ClientSecret: clientSecret,
		IsPublic:     false,
	}, nil
}

// CreateOAuth2PublicClient creates a new OAuth2 public client
func (c *Client) CreateOAuth2PublicClient(ctx context.Context, name, displayName, origin string) (*OAuth2Client, error) {
	req := NewCreateRequest(map[string]any{
		"name":                     []string{name},
		"displayname":              []string{displayName},
		"oauth2_rs_origin_landing": []string{origin},
	})

	resp, err := c.doRequest(ctx, "POST", "/v1/oauth2/_public", req)
	if err != nil {
		return nil, fmt.Errorf("create oauth2 public client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return &OAuth2Client{
		Name:        name,
		DisplayName: displayName,
		Origin:      origin,
		ClientID:    name,
		IsPublic:    true,
	}, nil
}

// GetOAuth2Client retrieves an OAuth2 client by name
func (c *Client) GetOAuth2Client(ctx context.Context, name string) (*OAuth2Client, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/oauth2/"+name, nil)
	if err != nil {
		return nil, fmt.Errorf("get oauth2 client: %w", err)
	}

	var entry Entry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, err
	}

	// Determine if public based on oauth2_rs_basic_secret attribute presence
	// Note: The value is hidden for basic clients, so we check if the key exists in attrs
	_, hasBasicSecret := entry.Attrs["oauth2_rs_basic_secret"]
	isPublic := !hasBasicSecret

	// Use 'name' attribute for the name (not oauth2_rs_name which is for internal use)
	clientName := entry.GetString("name")
	if clientName == "" {
		clientName = entry.GetString("oauth2_rs_name")
	}

	// Get origin and normalize by removing trailing slash if present
	// (Kanidm adds trailing slash, but Terraform configs typically don't have it)
	origin := entry.GetString("oauth2_rs_origin")
	if len(origin) > 0 && origin[len(origin)-1] == '/' {
		origin = origin[:len(origin)-1]
	}

	return &OAuth2Client{
		UUID:         firstNonEmpty(entry.GetString("entryuuid"), entry.GetString("uuid")),
		Name:         clientName,
		DisplayName:  entry.GetString("displayname"),
		Origin:       origin,
		RedirectURIs: entry.GetStringSlice("oauth2_rs_origin_landing"),
		ClientID:     clientName,
		IsPublic:     isPublic,
		// Note: Client secret is never returned in GET responses
	}, nil
}

// UpdateOAuth2Client updates an OAuth2 client. If newName is non-nil, the client is renamed.
func (c *Client) UpdateOAuth2Client(ctx context.Context, name string, newName *string, displayName, origin string, redirectURIs []string) error {
	attrs := make(map[string]any)

	if newName != nil {
		attrs["name"] = []string{*newName}
	}

	if displayName != "" {
		attrs["displayname"] = []string{displayName}
	}

	if origin != "" {
		attrs["oauth2_rs_origin"] = []string{origin}
	}

	if redirectURIs != nil {
		attrs["oauth2_rs_origin_landing"] = redirectURIs
	}

	req := NewUpdateRequest(attrs)

	resp, err := c.doRequest(ctx, "PATCH", "/v1/oauth2/"+name, req)
	if err != nil {
		return fmt.Errorf("update oauth2 client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// DeleteOAuth2Client deletes an OAuth2 client
func (c *Client) DeleteOAuth2Client(ctx context.Context, name string) error {
	resp, err := c.doRequest(ctx, "DELETE", "/v1/oauth2/"+name, nil)
	if err != nil {
		return fmt.Errorf("delete oauth2 client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// SetOAuth2ScopeMap sets the scope mapping for an OAuth2 client
func (c *Client) SetOAuth2ScopeMap(ctx context.Context, rsName, groupName string, scopes []string) error {
	// Send scopes array directly (not wrapped in an object)
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/oauth2/%s/_scopemap/%s", rsName, groupName), scopes)
	if err != nil {
		return fmt.Errorf("set oauth2 scope map: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// DeleteOAuth2ScopeMap removes a scope mapping for an OAuth2 client
func (c *Client) DeleteOAuth2ScopeMap(ctx context.Context, rsName, groupName string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/oauth2/%s/_scopemap/%s", rsName, groupName), nil)
	if err != nil {
		return fmt.Errorf("delete oauth2 scope map: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// GetOAuth2BasicSecret retrieves the client secret for a basic OAuth2 client
func (c *Client) GetOAuth2BasicSecret(ctx context.Context, name string) (string, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/v1/oauth2/%s/_basic_secret", name), nil)
	if err != nil {
		return "", fmt.Errorf("get oauth2 basic secret: %w", err)
	}

	// The API returns the secret as a plain JSON string
	var secret string
	if err := decodeResponse(resp, &secret); err != nil {
		return "", err
	}

	return secret, nil
}

// RegenerateOAuth2BasicSecret regenerates the client secret for a basic OAuth2 client
// This invalidates the old secret and generates a new one
func (c *Client) RegenerateOAuth2BasicSecret(ctx context.Context, name string) (string, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/oauth2/%s/_basic_secret", name), nil)
	if err != nil {
		return "", fmt.Errorf("regenerate oauth2 basic secret: %w", err)
	}

	// The API returns the new secret as a plain JSON string
	var secret string
	if err := decodeResponse(resp, &secret); err != nil {
		return "", err
	}

	return secret, nil
}

// UploadOAuth2Image uploads or replaces an OAuth2 client image.
func (c *Client) UploadOAuth2Image(ctx context.Context, name, imagePath string) error {
	fileContents, err := os.ReadFile(imagePath)
	if err != nil {
		return fmt.Errorf("read oauth2 image: %w", err)
	}

	contentType := mime.TypeByExtension(filepath.Ext(imagePath))
	if contentType == "" {
		return fmt.Errorf("determine oauth2 image content type for %q", imagePath)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="%s"`, filepath.Base(imagePath)))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create oauth2 image multipart part: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(fileContents)); err != nil {
		return fmt.Errorf("write oauth2 image multipart body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close oauth2 image multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+fmt.Sprintf("/v1/oauth2/%s/_image", name), &body)
	if err != nil {
		return fmt.Errorf("create oauth2 image request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute oauth2 image request: %w", err)
	}

	if err := c.checkResponse(resp); err != nil {
		return fmt.Errorf("upload oauth2 image: %w", err)
	}

	_ = resp.Body.Close()
	return nil
}

// DeleteOAuth2Image removes the image associated with an OAuth2 client.
func (c *Client) DeleteOAuth2Image(ctx context.Context, name string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/oauth2/%s/_image", name), nil)
	if err != nil {
		return fmt.Errorf("delete oauth2 image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}
