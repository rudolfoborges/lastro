package accounts

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestIssuedTokenParsesBackToItsKeyAndSecret(t *testing.T) {
	keyID := uuid.Must(uuid.NewV7())

	token, hash := issueToken(keyID)
	parsedID, secret, err := parseToken(token)

	if err != nil {
		t.Fatalf("parseToken() error = %v", err)
	}
	if parsedID != keyID {
		t.Errorf("key ID = %s, want %s", parsedID, keyID)
	}
	if !secretMatches(secret, hash) {
		t.Error("secret does not match the issued hash")
	}
	if strings.Contains(string(hash), token) || len(hash) != 32 {
		t.Errorf("hash must be a SHA-256 digest, got %d bytes", len(hash))
	}
}

func TestParseTokenRejectsMalformedTokens(t *testing.T) {
	valid, _ := issueToken(uuid.Must(uuid.NewV7()))
	for name, token := range map[string]string{
		"empty":          "",
		"wrong prefix":   "sk_" + strings.TrimPrefix(valid, tokenPrefix),
		"no separator":   strings.Replace(valid, "_", "", 2),
		"short key ID":   tokenPrefix + "abc_" + valid[len(valid)-43:],
		"bad hex":        tokenPrefix + strings.Repeat("z", 32) + "_" + valid[len(valid)-43:],
		"short secret":   valid[:len(valid)-4],
		"invalid base64": valid[:len(valid)-1] + "*",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseToken(token); err == nil {
				t.Errorf("parseToken(%q) error = nil", token)
			}
		})
	}
}

func TestSecretMatchesRejectsAnotherSecret(t *testing.T) {
	_, hash := issueToken(uuid.Must(uuid.NewV7()))
	if secretMatches(randomBytes(secretBytes), hash) {
		t.Error("a random secret matched the hash")
	}
}

func TestInviteCodesAreUniqueAndHashedConsistently(t *testing.T) {
	first, firstHash := issueInviteCode()
	second, _ := issueInviteCode()

	if first == second || !strings.HasPrefix(first, inviteCodePrefix) {
		t.Errorf("invite codes = %q, %q", first, second)
	}
	if string(inviteCodeHash(" "+first+"\n")) != string(firstHash) {
		t.Error("hash of the trimmed code differs from the issued hash")
	}
}
