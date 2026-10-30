// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package metrics

import (
	"fmt"

	"github.com/zextras/carbonio-configd/internal/sys"
)

// textfileMode makes the file readable by the node_exporter user.
const textfileMode = 0o644

// WriteTextfile atomically replaces path with the registry in the Prometheus
// text format. The node_exporter textfile collector only reads files ending in
// ".prom"; configure the path accordingly. Nothing is written for a nil
// registry.
func WriteTextfile(fsys sys.FS, path string, r *Registry) error {
	if r == nil {
		return nil
	}

	if err := fsys.WriteFileAtomic(path, r.Render(), textfileMode); err != nil {
		return fmt.Errorf("write metrics textfile %s: %w", path, err)
	}

	return nil
}
