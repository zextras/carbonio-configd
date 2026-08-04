// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package localconfig

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// passwordCharset is the alphabet used for random password generation. It is
// the 64-entry ALPHABET of the Java com.zimbra.common.util.RandomPassword,
// which "zmlocalconfig -r" used before configd took over the localconfig CLI.
//
// The set MUST stay free of shell metacharacters: callers such as
// /opt/zextras/libexec/zmmyinit interpolate the generated value straight into
// `su - zextras -c "... zmmypasswd $pw"`, so a password containing `(`, `&`,
// `}` or `>` aborts the caller with a bash syntax error instead of setting the
// password.
const passwordCharset = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_."

// GeneratePassword creates a cryptographically secure random password
// of the given length using passwordCharset.
func GeneratePassword(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("password length must be positive, got %d", length)
	}

	result := make([]byte, length)
	charsetLen := big.NewInt(int64(len(passwordCharset)))

	for i := range length {
		idx, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			return "", fmt.Errorf("failed to generate random byte: %w", err)
		}

		result[i] = passwordCharset[idx.Int64()]
	}

	return string(result), nil
}
