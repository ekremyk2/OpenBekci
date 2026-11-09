package governance

import (
	"reflect"
	"testing"
)

// Every document string is filled in both languages.
func TestDocumentMsgsComplete(t *testing.T) {
	for name, m := range map[string]msgs{"tr": trMsgs, "en": enMsgs} {
		v := reflect.ValueOf(m)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() == reflect.String && v.Field(i).String() == "" {
				t.Errorf("%s: %s is empty", name, v.Type().Field(i).Name)
			}
		}
	}
}
