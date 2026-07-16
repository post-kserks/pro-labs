package security

import (
	"testing"
)

func TestMaskString(t *testing.T) {
	tests := []struct {
		name   string
		val    string
		policy MaskingPolicy
		want   string
	}{
		{
			name:   "standard mask",
			val:    "hello world",
			policy: MaskingPolicy{Prefix: 2, Suffix: 2, Char: '*'},
			want:   "he*******ld",
		},
		{
			name:   "empty string",
			val:    "",
			policy: MaskingPolicy{Prefix: 1, Suffix: 1, Char: '*'},
			want:   "",
		},
		{
			name:   "short string",
			val:    "hi",
			policy: MaskingPolicy{Prefix: 2, Suffix: 2, Char: '*'},
			want:   "hi",
		},
		{
			name:   "unicode string",
			val:    "привет мир",
			policy: MaskingPolicy{Prefix: 2, Suffix: 2, Char: 'x'},
			want:   "прxxxxxxир",
		},
		{
			name:   "no mask characters",
			val:    "test",
			policy: MaskingPolicy{Prefix: 4, Suffix: 0, Char: '*'},
			want:   "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskString(tt.val, tt.policy); got != tt.want {
				t.Errorf("MaskString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRegistry(t *testing.T) {
	RegisterPolicy("db1", "t1", "c1", MaskingPolicy{Prefix: 1, Suffix: 1, Char: '*'})
	p, ok := GetPolicy("db1", "t1", "c1")
	if !ok {
		t.Fatal("expected policy to be registered")
	}
	if p.Char != '*' {
		t.Errorf("unexpected policy char: %v", p.Char)
	}

	_, ok = GetPolicy("db1", "t1", "c2")
	if ok {
		t.Fatal("expected no policy for c2")
	}
}
