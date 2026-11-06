// Package auth is login for the UI: password hashing, sessions and the brake on guessing.
//
// Passwords are hashed with argon2id and kept as PHC strings, which carry their own parameters, so the parameters can
// be raised later without a migration. A session is a random token held by the browser and known to the database only
// by its SHA-256.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidHash means a stored password hash is not an argon2id PHC string this package can check, or asks for more
// work than it accepts. It is never a match.
var ErrInvalidHash = errors.New("invalid password hash")

// ErrInvalidParams means hashing parameters that argon2id cannot use, or that VerifyPassword would refuse.
var ErrInvalidParams = errors.New("invalid argon2id parameters")

// Params are the cost of an argon2id hash.
type Params struct {
	// Time is the number of passes over the memory.
	Time uint32
	// Memory is the memory used, in KiB.
	Memory uint32
	// Threads is the degree of parallelism.
	Threads uint8
}

// DefaultParams are the argon2id parameters of RFC 9106 for a memory-constrained setting: 64 MiB, three passes.
var DefaultParams = Params{Time: 3, Memory: 64 * 1024, Threads: 4}

const (
	saltLength = 16
	keyLength  = 32
	// The most work VerifyPassword does for a stored hash, so that a damaged or hostile row cannot make the server
	// allocate gigabytes or spin for minutes. They are far above DefaultParams.
	maxMemory  = 256 * 1024 // KiB
	maxTime    = 16
	maxThreads = 32
	maxKeySize = 128
)

func (p Params) validate() error {
	switch {
	case p.Time < 1 || p.Time > maxTime:
		return fmt.Errorf("%w: time %d is not between 1 and %d", ErrInvalidParams, p.Time, maxTime)
	case p.Threads < 1 || p.Threads > maxThreads:
		return fmt.Errorf("%w: threads %d is not between 1 and %d", ErrInvalidParams, p.Threads, maxThreads)
	case p.Memory < 8*uint32(p.Threads) || p.Memory > maxMemory:
		return fmt.Errorf("%w: memory %d KiB is not between %d and %d", ErrInvalidParams, p.Memory, 8*uint32(p.Threads), maxMemory)
	}
	return nil
}

// HashPassword returns the argon2id hash of password as a PHC string, with a new random salt.
func HashPassword(password string, p Params) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, keyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password is the one that hash was made from. A hash it cannot read is an error
// wrapping ErrInvalidHash, never a match.
func VerifyPassword(hash, password string) (bool, error) {
	p, salt, key, err := parseHash(hash)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// parseHash reads a PHC string: $argon2id$v=19$m=<KiB>,t=<passes>,p=<threads>$<salt>$<key>, in unpadded base64.
func parseHash(hash string) (p Params, salt, key []byte, err error) {
	bad := func(why string) (Params, []byte, []byte, error) {
		return Params{}, nil, nil, fmt.Errorf("%w: %s", ErrInvalidHash, why)
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return bad("not an argon2id PHC string")
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return bad("unsupported argon2 version " + parts[2])
	}
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return bad("the parameters are not m, t and p")
	}
	var values [3]uint64
	for i, name := range []string{"m=", "t=", "p="} {
		num, ok := strings.CutPrefix(fields[i], name)
		if !ok {
			return bad("the parameters are not m, t and p")
		}
		n, perr := strconv.ParseUint(num, 10, 32)
		if perr != nil {
			return bad("the parameter " + name + " is not a number")
		}
		values[i] = n
	}
	if values[2] > maxThreads {
		return bad("too many threads")
	}
	p = Params{Memory: uint32(values[0]), Time: uint32(values[1]), Threads: uint8(values[2])}
	if verr := p.validate(); verr != nil {
		return bad(verr.Error())
	}
	if salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4]); err != nil || len(salt) == 0 {
		return bad("the salt is not base64")
	}
	if key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5]); err != nil || len(key) == 0 || len(key) > maxKeySize {
		return bad("the key is not base64 or has a strange size")
	}
	return p, salt, key, nil
}
