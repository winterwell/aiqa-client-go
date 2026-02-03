package aiqa

import (
	"testing"
)

func TestParseLLMResponse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    float64
		wantErr bool
	}{
		{"valid", `{"score": 0.75, "message": "ok"}`, 0.75, false},
		{"valid int", `{"score": 1, "message": ""}`, 1.0, false},
		{"clamp high", `{"score": 1.5}`, 1.0, false},
		{"clamp low", `{"score": -0.1}`, 0.0, false},
		{"missing score", `{"message": "nope"}`, 0, true},
		{"invalid json", `not json`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLLMResponse(tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLLMResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Score != tt.want {
				t.Errorf("ParseLLMResponse().Score = %v, want %v", got.Score, tt.want)
			}
		})
	}
}

func TestToJSONOrString(t *testing.T) {
	if toJSONOrString(nil) != "" {
		t.Error("nil should return empty string")
	}
	if toJSONOrString("hello") != "hello" {
		t.Error("string should pass through")
	}
	m := map[string]interface{}{"a": 1}
	s := toJSONOrString(m)
	if s != `{"a":1}` && s != `{"a": 1}` {
		t.Errorf("map should JSON marshal, got %q", s)
	}
}
