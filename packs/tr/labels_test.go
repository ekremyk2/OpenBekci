package tr

import "testing"

func TestLabelsMatchInBothLocales(t *testing.T) {
	for k, v := range labelsEN {
		if v == "" || labelsTR[k] == "" {
			t.Errorf("label %s missing or empty in tr", k)
		}
	}
	for k := range labelsTR {
		if labelsEN[k] == "" {
			t.Errorf("label %s missing in en", k)
		}
	}
}
