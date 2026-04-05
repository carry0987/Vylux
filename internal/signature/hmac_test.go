package signature

import "testing"

func TestCanonicalizeImageOptions(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "already canonical", raw: "w300_h200_q80", want: "w300_h200_q80"},
		{name: "reorders tokens", raw: "q80_h200_w300", want: "w300_h200_q80"},
		{name: "preserves omitted values", raw: "h200_w300", want: "w300_h200"},
		{name: "empty", raw: "", want: ""},
		{name: "invalid token", raw: "x100", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalizeImageOptions(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("CanonicalizeImageOptions(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCanonicalizeImageSourcePath(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "encoded space", raw: "uploads/My%20Image.png.webp", want: "uploads/My Image.png.webp"},
		{name: "decoded space", raw: "uploads/My Image.png.webp", want: "uploads/My Image.png.webp"},
		{name: "jpeg normalized", raw: "uploads/demo.jpg.jpeg", want: "uploads/demo.jpg.jpg"},
		{name: "missing ext", raw: "uploads/demo", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalizeImageSourcePath(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("CanonicalizeImageSourcePath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestVerifyImageCanonicalization(t *testing.T) {
	secret := "test-secret"
	sig, err := SignImage(secret, "h200_w300", "uploads/My Image.png.jpg")
	if err != nil {
		t.Fatalf("SignImage returned error: %v", err)
	}

	ok, err := VerifyImage(secret, sig, "w300_h200", "uploads/My%20Image.png.jpg")
	if err != nil {
		t.Fatalf("VerifyImage returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected signature to verify after canonicalization")
	}

	ok, err = VerifyImage(secret, sig, "w300_h200", "uploads/My Image.png.jpeg")
	if err != nil {
		t.Fatalf("VerifyImage returned error for jpeg alias: %v", err)
	}
	if !ok {
		t.Fatal("expected jpg/jpeg aliases to verify after canonicalization")
	}

	ok, err = VerifyImage(secret, sig, "w400_h200", "uploads/My Image.png.jpg")
	if err != nil {
		t.Fatalf("VerifyImage returned error for tampered options: %v", err)
	}
	if ok {
		t.Fatal("expected tampered options to fail verification")
	}
}

func TestCanonicalizeObjectKey(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "encoded slash and space", raw: "uploads%2FMy%20Image.png", want: "uploads/My Image.png"},
		{name: "decoded key", raw: "uploads/My Image.png", want: "uploads/My Image.png"},
		{name: "leading slash trimmed", raw: "/uploads/demo.png", want: "uploads/demo.png"},
		{name: "empty", raw: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalizeObjectKey(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.raw)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("CanonicalizeObjectKey(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestVerifyOriginalCanonicalization(t *testing.T) {
	secret := "test-secret"
	sig, err := SignOriginal(secret, "uploads/My Image.png")
	if err != nil {
		t.Fatalf("SignOriginal returned error: %v", err)
	}

	ok, err := VerifyOriginal(secret, sig, "uploads%2FMy%20Image.png")
	if err != nil {
		t.Fatalf("VerifyOriginal returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected original signature to verify after canonicalization")
	}

	ok, err = VerifyOriginal(secret, sig, "uploads%2FOther%20Image.png")
	if err != nil {
		t.Fatalf("VerifyOriginal returned error for tampered key: %v", err)
	}
	if ok {
		t.Fatal("expected tampered object key to fail verification")
	}
}
