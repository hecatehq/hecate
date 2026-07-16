package catalog

import "testing"

func TestResolveProviderIdentityPrefersCanonicalNameOverAlias(t *testing.T) {
	t.Parallel()

	resolution := ResolveProviderIdentity([]ProviderIdentity{
		{Name: "vision-a", Aliases: []string{"vision-b"}},
		{Name: "vision-b", Aliases: []string{"provider-b"}},
	}, "vision-b")

	if !resolution.Found || resolution.Ambiguous || resolution.Index != 1 {
		t.Fatalf("ResolveProviderIdentity() = %+v, want unambiguous canonical provider at index 1", resolution)
	}
}

func TestResolveProviderIdentityPrefersNormalizedCanonicalNameOverAlias(t *testing.T) {
	t.Parallel()

	resolution := ResolveProviderIdentity([]ProviderIdentity{
		{Name: "vision-a", Aliases: []string{"vision production"}},
		{Name: "Vision Production", Aliases: []string{"provider-b"}},
	}, "vision-production")

	if !resolution.Found || resolution.Ambiguous || resolution.Index != 1 {
		t.Fatalf("ResolveProviderIdentity() = %+v, want unambiguous normalized canonical provider at index 1", resolution)
	}
}

func TestResolveProviderIdentityRejectsAmbiguousAlias(t *testing.T) {
	t.Parallel()

	resolution := ResolveProviderIdentity([]ProviderIdentity{
		{Name: "vision-a", Aliases: []string{"shared-vision"}},
		{Name: "vision-b", Aliases: []string{"shared-vision"}},
	}, "shared-vision")

	if !resolution.Found || !resolution.Ambiguous {
		t.Fatalf("ResolveProviderIdentity() = %+v, want ambiguous alias match", resolution)
	}
}
