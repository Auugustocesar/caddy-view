package sites

import (
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/Auugustocesar/caddy-view/internal/store"
)

// PlaceholderHash is a hash of the shape bcrypt makes, standing for the real one in a preview: the real hash is made
// when the site is saved, and a preview must not cost a hash every time something is typed.
const PlaceholderHash = "$2a$10$....................................................."

const (
	minPasswordBytes = 8
	// maxPasswordBytes is what bcrypt reads. Newer versions refuse more instead of cutting it, and so does this.
	maxPasswordBytes = 72
)

var (
	// userPattern is what a username may be: nothing that Caddy, an HTTP header or a Caddyfile would read as something else.
	userPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)
	// hashPattern is the whole of what bcrypt writes: its version, a cost from 04 to 31, and 53 characters of its alphabet.
	// Nothing else may reach the managed file, whatever a row holds.
	hashPattern = regexp.MustCompile(`^\$2[aby]\$(0[4-9]|[12][0-9]|3[01])\$[./A-Za-z0-9]{53}$`)
)

const userMessage = "Use 1 to 64 letters, digits and . _ - @ in the username."

// validateBasicAuth judges the user and the hash of a site that has been normalised.
func validateBasicAuth(p *problems, n store.Site) {
	if n.BasicAuthUser == "" && n.BasicAuthHash == "" {
		return
	}
	if n.Kind == store.SiteRedirect {
		p.add("basicAuthUser", "Only a proxy can ask for a password.")
	}
	switch {
	case n.BasicAuthUser == "":
		p.add("basicAuthUser", "Enter a username for the password.")
	case !userPattern.MatchString(n.BasicAuthUser):
		p.add("basicAuthUser", userMessage)
	}
	switch {
	case n.BasicAuthHash == "":
		p.add("basicAuthPassword", "Enter a password.")
	case !hashPattern.MatchString(n.BasicAuthHash):
		p.add("basicAuthPassword", "The stored password is not a bcrypt hash: enter the password again.")
	}
}

// SetBasicAuth sets the basic auth of s from what a request says: the username, and the password in clear. An empty
// username takes the basic auth away. A password makes a new hash, with the given bcrypt cost (zero is bcrypt's default);
// without one, the hash of previous is kept, which needs previous to have one. On any problem s is left as it was.
func SetBasicAuth(s *store.Site, user, password string, previous *store.Site, cost int) []FieldError {
	user = strings.TrimSpace(user)
	if user == "" {
		if password != "" {
			return []FieldError{{Field: "basicAuthUser", Message: "Enter a username for the password, or leave the password empty."}}
		}
		s.BasicAuthUser, s.BasicAuthHash = "", ""
		return nil
	}

	var p problems
	if !userPattern.MatchString(user) {
		p.add("basicAuthUser", userMessage)
	}
	hash := ""
	switch {
	case password != "":
		switch {
		case len(password) < minPasswordBytes:
			p.add("basicAuthPassword", "The password needs at least 8 characters.")
		case len(password) > maxPasswordBytes:
			p.add("basicAuthPassword", "The password can be at most 72 bytes: about 72 letters, fewer with accents.")
		}
	case previous != nil && previous.BasicAuthHash != "":
		hash = previous.BasicAuthHash
	default:
		p.add("basicAuthPassword", "Enter a password.")
	}
	if p.list != nil {
		return p.list
	}

	if password != "" {
		made, err := bcrypt.GenerateFromPassword([]byte(password), cost)
		if err != nil {
			return []FieldError{{Field: "basicAuthPassword", Message: "The password could not be hashed. Try another one."}}
		}
		hash = string(made)
	}
	s.BasicAuthUser, s.BasicAuthHash = user, hash
	return nil
}
