package provider

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	p "github.com/pulumi/pulumi-go-provider"
	"github.com/pulumi/pulumi-go-provider/infer"

	"github.com/bambamboole/pulumi-provider-glitchtip/internal/glitchtip"
)

// clientKey carries a client injected directly into the context; tests use it
// to exercise resource methods without a configured provider.
type clientKey struct{}

// client returns the GlitchTip API client configured by the provider.
func client(ctx context.Context) *glitchtip.Client {
	if c, ok := ctx.Value(clientKey{}).(*glitchtip.Client); ok {
		return c
	}
	return infer.GetConfig[Config](ctx).client
}

// diffArgs compares the pulumi-tagged fields of old and new argument structs
// and returns a property diff per changed field. Fields listed in replaces
// trigger a replacement instead of an update. Nil and empty slices or maps are
// considered equal, so an omitted list never diffs against an empty one.
func diffArgs[A any](old, new A, replaces ...string) map[string]p.PropertyDiff {
	diff := map[string]p.PropertyDiff{}
	oldValue, newValue := reflect.ValueOf(old), reflect.ValueOf(new)
	typ := oldValue.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("pulumi")
		if tag == "" || !field.IsExported() {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if equalValues(oldValue.Field(i), newValue.Field(i)) {
			continue
		}
		kind := p.Update
		for _, replace := range replaces {
			if replace == name {
				kind = p.UpdateReplace
			}
		}
		diff[name] = p.PropertyDiff{Kind: kind, InputDiff: true}
	}
	return diff
}

func equalValues(a, b reflect.Value) bool {
	switch a.Kind() {
	case reflect.Slice, reflect.Map:
		if a.Len() == 0 && b.Len() == 0 {
			return true
		}
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// diffResponse builds the infer response for a detailed diff.
func diffResponse(diff map[string]p.PropertyDiff) infer.DiffResponse {
	return infer.DiffResponse{HasChanges: len(diff) > 0, DetailedDiff: diff}
}

// splitID splits a slash-joined resource ID into exactly n segments.
func splitID(id string, n int, format string) ([]string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != n {
		return nil, fmt.Errorf("glitchtip: resource ID %q must have the form %s", id, format)
	}
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("glitchtip: resource ID %q must have the form %s", id, format)
		}
	}
	return parts, nil
}

// deref returns the string behind an optional API field.
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// optional returns nil for an empty string so the API clears the field.
func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
