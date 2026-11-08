package fixture

import "strings"

// Value is one secret-shaped string a test plants in a fixture and then hunts for in every output.
type Value struct {
	Name string
	Text string
}

// leakWindow is the length of the substrings Leaks looks for: a value that leaks in part still leaks.
const leakWindow = 8

// join assembles a value from parts, so that no secret-shaped literal is ever committed.
func join(parts ...string) string { return strings.Join(parts, "") }

// AWSKey is a cloud access key id, assembled at run time.
func AWSKey() Value {
	return Value{"aws", join("AK", "IA", "Q7W2", "E3R4", "T5Y6", "U7I2")}
}

// GitHubToken is a forge token, assembled at run time.
func GitHubToken() Value {
	return Value{"github", join("gh", "p_", "k3J9xQ2mZ", "7vB5nR8tY", "1wL4cH6dF", "0gS3aPe7u")}
}

// NPMToken is a package registry token, assembled at run time.
func NPMToken() Value {
	return Value{"npm", join("np", "m_", "Xr4Tq8Nw2", "Lm6Vb1Zc5", "Hd9Kf3Js7", "Pa0Gy2Ue8")}
}

// GitLabToken is a forge token, assembled at run time.
func GitLabToken() Value {
	return Value{"gitlab", join("gl", "pat-", "Q4m8Z2x7N1", "c5V9b3L6k0")}
}

// Password is the value of an assignment to a name that ends in a secret word.
func Password() Value {
	return Value{"password", join("Hq7!", "vN2z", "Xp9R", "k4Lm")}
}

// PlainPassword is a password of letters and digits only, the shape a .env or YAML file usually holds.
func PlainPassword() Value {
	return Value{"plain-password", join("pW", "x7Kq", "2mZv", "9RtB", "4nLs")}
}

// SigningKey is a 40-character secret key of the kind a cloud or framework setting holds.
func SigningKey() Value {
	return Value{"signing-key", join("wJalrXUtnFEMI", "K7MDENGbPxRfi", "CYa1b2c3d4e5f", "gH")}
}

// KeyHeader opens a private key block.
func KeyHeader() string { return join("-----BE", "GIN RSA PRIV", "ATE KEY-----") }

// KeyFooter closes a private key block.
func KeyFooter() string { return join("-----E", "ND RSA PRIV", "ATE KEY-----") }

// KeyBody returns the lines of a private key block, which are its secret.
func KeyBody() []Value {
	return []Value{
		{"key-line-1", join("MIIEowIBAAKCAQEA7v3k", "9QpLzX1mN4bV8cR2tY6wHgF0", "dS5aJeUq3oPiKl7Zx9CvBn1M")},
		{"key-line-2", join("r8Tw4EyU2iOpAsDfGhJkLzXc", "VbNm1Q3W5e7R9tYuIoPaSdFg", "HjKlZxCvBn2M4q6w8e0rTyUi")},
	}
}

// Leaks returns the name of every value that appears in haystack, whole or by any leakWindow-byte part of it.
func Leaks(haystack string, values ...Value) []string {
	var hit []string
	for _, v := range values {
		if strings.Contains(haystack, v.Text) {
			hit = append(hit, v.Name)
			continue
		}
		for i := 0; i+leakWindow <= len(v.Text); i++ {
			if strings.Contains(haystack, v.Text[i:i+leakWindow]) {
				hit = append(hit, v.Name)
				break
			}
		}
	}
	return hit
}
