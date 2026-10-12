package mcpstdio

import (
	"context"
	"x-science/internal/persistence/productstate"
)

// UpgradePrivateNamespace must precede OAuth initialization and background work.
func UpgradePrivateNamespace(root string) error {
	return UpgradePrivateNamespaceContext(context.Background(), root)
}

func UpgradePrivateNamespaceContext(ctx context.Context, root string) error {
	return productstate.UpgradeHiddenNamespace(ctx, root)
}

func ensurePrivateNamespace(root string) error {
	return productstate.EnsureHiddenNamespace(context.Background(), root)
}
