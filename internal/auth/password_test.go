package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Auugustocesar/caddy-view/internal/auth"
)

// cheap are the least expensive valid parameters, so the tests do not spend their time hashing.
var cheap = auth.Params{Time: 1, Memory: 8, Threads: 1}

func TestHashPasswordRoundTrips(t *testing.T) {
	hash, err := auth.HashPassword("correct horse", cheap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Errorf("hash = %q, want an argon2id PHC string carrying its parameters", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Error("the hash contains the password")
	}
	if ok, err := auth.VerifyPassword(hash, "correct horse"); err != nil || !ok {
		t.Errorf("VerifyPassword with the right password = %v, %v, want true", ok, err)
	}
	if ok, err := auth.VerifyPassword(hash, "correct horse "); err != nil || ok {
		t.Errorf("VerifyPassword with a wrong password = %v, %v, want false and no error", ok, err)
	}
}

func TestHashPasswordUsesANewSaltEachTime(t *testing.T) {
	a, errA := auth.HashPassword("same", cheap)
	b, errB := auth.HashPassword("same", cheap)
	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}
	if a == b {
		t.Error("two hashes of one password are equal, so the salt is not random")
	}
}

func TestVerifyPasswordUsesTheParametersOfTheHash(t *testing.T) {
	// A hash made with other parameters still verifies: that is what lets the parameters be raised later.
	old, err := auth.HashPassword("pw", auth.Params{Time: 2, Memory: 16, Threads: 2})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := auth.VerifyPassword(old, "pw"); err != nil || !ok {
		t.Errorf("VerifyPassword of a hash with other parameters = %v, %v, want true", ok, err)
	}
}

func TestVerifyPasswordRefusesWhatIsNotAHash(t *testing.T) {
	good, err := auth.HashPassword("pw", cheap)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(good, "$") // "", argon2id, v=19, m=..., salt, key
	cases := map[string]string{
		"empty":             "",
		"plain text":        "pw",
		"another algorithm": "$bcrypt$v=19$m=8,t=1,p=1$" + parts[4] + "$" + parts[5],
		"argon2i":           strings.Replace(good, "argon2id", "argon2i", 1),
		"another version":   strings.Replace(good, "v=19", "v=16", 1),
		"missing key":       strings.Join(parts[:5], "$"),
		"bad parameters":    "$argon2id$v=19$m=x,t=1,p=1$" + parts[4] + "$" + parts[5],
		"zero time":         "$argon2id$v=19$m=8,t=0,p=1$" + parts[4] + "$" + parts[5],
		"bad salt":          "$argon2id$v=19$m=8,t=1,p=1$!!!$" + parts[5],
		"bad key":           "$argon2id$v=19$m=8,t=1,p=1$" + parts[4] + "$!!!",
		"empty key":         "$argon2id$v=19$m=8,t=1,p=1$" + parts[4] + "$",
		// A stored hash must not be able to make the server allocate gigabytes.
		"memory too large": "$argon2id$v=19$m=4194304,t=1,p=1$" + parts[4] + "$" + parts[5],
		"time too large":   "$argon2id$v=19$m=8,t=1000,p=1$" + parts[4] + "$" + parts[5],
	}
	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := auth.VerifyPassword(hash, "pw")
			if ok || !errors.Is(err, auth.ErrInvalidHash) {
				t.Errorf("VerifyPassword(%q) = %v, %v, want false and ErrInvalidHash", hash, ok, err)
			}
		})
	}
}

func TestHashPasswordRefusesImpossibleParameters(t *testing.T) {
	for name, p := range map[string]auth.Params{
		"zero":              {},
		"no time":           {Time: 0, Memory: 8, Threads: 1},
		"no threads":        {Time: 1, Memory: 8, Threads: 0},
		"too little memory": {Time: 1, Memory: 7, Threads: 1},
		"memory per thread": {Time: 1, Memory: 15, Threads: 2},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := auth.HashPassword("pw", p); !errors.Is(err, auth.ErrInvalidParams) {
				t.Errorf("HashPassword with %+v: error = %v, want ErrInvalidParams", p, err)
			}
		})
	}
}

func TestDefaultParamsAreValid(t *testing.T) {
	if _, err := auth.HashPassword("pw", auth.DefaultParams); err != nil {
		t.Errorf("HashPassword with DefaultParams: %v", err)
	}
}
