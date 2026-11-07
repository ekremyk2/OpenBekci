package tickets

import "testing"

func TestValidateStatuses(t *testing.T) {
	ok := []StatusInput{{Name: "New", Semantic: OpenLike}, {Name: "Waiting for parts", Semantic: OpenLike}, {Name: "Done", Semantic: ClosedLike}}
	if err := validateStatuses(ok); err != nil {
		t.Fatalf("valid workflow refused: %v", err)
	}
	for name, in := range map[string][]StatusInput{
		"empty":          nil,
		"no closed-like": {{Name: "New", Semantic: OpenLike}},
		"no open-like":   {{Name: "Done", Semantic: ClosedLike}},
		"duplicate name": {{Name: "New", Semantic: OpenLike}, {Name: "new", Semantic: ClosedLike}},
		"bad semantic":   {{Name: "New", Semantic: "open"}, {Name: "Done", Semantic: ClosedLike}},
		"blank name":     {{Name: "  ", Semantic: OpenLike}, {Name: "Done", Semantic: ClosedLike}},
	} {
		if err := validateStatuses(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
