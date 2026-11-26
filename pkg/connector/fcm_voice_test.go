package connector

import (
	"encoding/json"
	"strconv"
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

func TestEmoticonMapComplete(t *testing.T) {
	seen := map[string]int{}
	for id := 1001; id <= 1024; id++ {
		e, ok := xploraEmoticonMap[id]
		if !ok {
			t.Errorf("sticker %d has no emoji", id)
			continue
		}
		if prev, dup := seen[e]; dup {
			t.Errorf("emoji %s used for both %d and %d", e, prev, id)
		}
		seen[e] = id
		if got := emojiToXploraEmoticon[e]; got != "M"+strconv.Itoa(id) {
			t.Errorf("reverse map %s = %q, want M%d", e, got, id)
		}
	}
}
