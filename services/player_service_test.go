package services

import "testing"

func TestNormalizeProfileUUID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "compact",
			in:   "00112233445566778899aabbccddeeff",
			want: "00112233445566778899aabbccddeeff",
		},
		{
			name: "canonical with hyphens",
			in:   "00112233-4455-6677-8899-aabbccddeeff",
			want: "00112233445566778899aabbccddeeff",
		},
		{
			name: "uppercase",
			in:   "00112233-4455-6677-8899-AABBCCDDEEFF",
			want: "00112233445566778899aabbccddeeff",
		},
		{name: "misplaced hyphen", in: "001122334455-66778899aabbccddeeff"},
		{name: "non hexadecimal", in: "00112233445566778899aabbccddeefg"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeProfileUUID(test.in); got != test.want {
				t.Fatalf("normalizeProfileUUID(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}
