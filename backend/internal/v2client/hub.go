package v2client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// HubJSON shares transport validation but only exposes the Hub route family.
func (c *Client) HubJSON(ctx context.Context, method, path string, in any) (json.RawMessage, error) {
	if !strings.HasPrefix(path, "/v1/hub/") || strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("invalid Hub route")
	}
	var out json.RawMessage
	err := c.doJSON(ctx, method, path, in, &out)
	return out, err
}
