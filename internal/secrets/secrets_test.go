package secrets

import "testing"

func TestSealOpenRoundTrip(t *testing.T) {
	ciphertext, err := Seal("secret-key", "provider-token")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := Open("secret-key", ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "provider-token" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if _, err := Open("other-secret-key", ciphertext); err == nil {
		t.Fatal("Open with wrong key succeeded")
	}
}
