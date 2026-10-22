package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
)

const (
	tokenPrefix      = "lst_"
	inviteCodePrefix = "inv_"
	secretBytes      = 32
	inviteCodeBytes  = 24
)

var errMalformedToken = errors.New("malformed API key")

type Scope string

const (
	ScopeAdmin           Scope = "admin"
	ScopeExpensesRead    Scope = "expenses:read"
	ScopeExpensesWrite   Scope = "expenses:write"
	ScopeWalletIngest    Scope = "wallet:ingest"
	ScopeStatementsWrite Scope = "statements:write"
)

var AllScopes = []Scope{ScopeAdmin, ScopeExpensesRead, ScopeExpensesWrite, ScopeWalletIngest, ScopeStatementsWrite}

func (s Scope) valid() bool {
	for _, known := range AllScopes {
		if s == known {
			return true
		}
	}
	return false
}

func issueToken(keyID uuid.UUID) (token string, hash []byte) {
	secret := randomBytes(secretBytes)
	token = tokenPrefix + hex.EncodeToString(keyID[:]) + "_" + base64.RawURLEncoding.EncodeToString(secret)
	return token, digest(secret)
}

func parseToken(token string) (keyID uuid.UUID, secret []byte, err error) {
	rest, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok {
		return uuid.Nil, nil, errMalformedToken
	}
	encodedID, encodedSecret, ok := strings.Cut(rest, "_")
	if !ok || len(encodedID) != hex.EncodedLen(len(keyID)) {
		return uuid.Nil, nil, errMalformedToken
	}
	if _, err := hex.Decode(keyID[:], []byte(encodedID)); err != nil {
		return uuid.Nil, nil, errMalformedToken
	}
	secret, err = base64.RawURLEncoding.DecodeString(encodedSecret)
	if err != nil || len(secret) != secretBytes {
		return uuid.Nil, nil, errMalformedToken
	}
	return keyID, secret, nil
}

func secretMatches(secret, hash []byte) bool {
	return subtle.ConstantTimeCompare(digest(secret), hash) == 1
}

func issueInviteCode() (code string, hash []byte) {
	code = inviteCodePrefix + base64.RawURLEncoding.EncodeToString(randomBytes(inviteCodeBytes))
	return code, digest([]byte(code))
}

func inviteCodeHash(code string) []byte {
	return digest([]byte(strings.TrimSpace(code)))
}

func digest(value []byte) []byte {
	sum := sha256.Sum256(value)
	return sum[:]
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return b
}
