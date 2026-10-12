#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
product_version="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$root/product-identity.json")"
tmp="$(mktemp -d)"
cleanup() {
	case "$tmp" in
	/tmp/tmp.*) rm -rf -- "$tmp" ;;
	*) echo "refusing to remove unexpected identity test path" >&2; return 1 ;;
	esac
}
trap cleanup EXIT

mkdir -p "$tmp/trusted/scripts" "$tmp/current" "$tmp/legacy" "$tmp/symlink"
cp "$root/scripts/release-path-policy.sh" "$tmp/trusted/scripts/release-path-policy.sh"
cp "$root/product-identity.json" "$tmp/trusted/product-identity.json"
cp "$root/product-identity.json" "$tmp/current/product-identity.json"
sed "s/\"version\": \"${product_version}\"/\"version\": \"4.0.2\"/" "$root/product-identity.json" >"$tmp/legacy/product-identity.json"
ln -s "$tmp/trusted/product-identity.json" "$tmp/symlink/product-identity.json"

source "$tmp/trusted/scripts/release-path-policy.sh"
product_name="$(x_science_release_product_name)"
[[ "$product_name" == "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["display_name"])' "$root/product-identity.json")" ]]
x_science_release_assert_product_identity "$tmp/current"
if x_science_release_assert_product_identity "$tmp/legacy" >/dev/null 2>&1; then
	echo "identity policy accepted a legacy product version" >&2
	exit 1
fi
if x_science_release_assert_product_identity "$tmp/symlink" >/dev/null 2>&1; then
	echo "identity policy accepted a symbolic-link identity" >&2
	exit 1
fi

# Ambiguous authority lines cannot be turned into a misleading install label.
cp "$root/product-identity.json" "$tmp/trusted/product-identity.json"
sed -n '/"display_name"/p' "$root/product-identity.json" >>"$tmp/trusted/product-identity.json"
if x_science_release_product_name >/dev/null 2>&1; then
	echo "identity policy accepted ambiguous display names" >&2
	exit 1
fi
cp "$root/product-identity.json" "$tmp/trusted/product-identity.json"

echo "RELEASE_IDENTITY_POLICY_TEST=yes"
