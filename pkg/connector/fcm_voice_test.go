package connector

import (
	"encoding/json"
	"testing"
)

func TestFlexIntVoiceDuration(t *testing.T) {
	var v struct {
		D flexInt `json:"duration"`
	}
	for in, want := range map[string]int{`{"duration":"5"}`: 5, `{"duration":7}`: 7, `{"duration":"x"}`: 0, `{}`: 0} {
		v.D = 0
		if err := json.Unmarshal([]byte(in), &v); err != nil || int(v.D) != want {
			t.Errorf("%s: got %d err %v, want %d", in, v.D, err, want)
		}
	}
}
