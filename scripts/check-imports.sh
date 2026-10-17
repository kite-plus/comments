#!/bin/sh
# Enforce the dependency rules in docs/design/architecture.md section 8.
# An import is all it takes to break a layering rule, so the rules are checked
# rather than trusted.
set -eu

MODULE=github.com/kite-plus/comments
status=0

fail() {
    echo "FAIL: $1"
    status=1
}

# deps PKG prints every package PKG depends on, transitively.
deps() {
    go list -deps "./$1" 2>/dev/null || true
}

# reaches DEPS PREFIX succeeds when DEPS contains PREFIX or a package below it.
reaches() {
    printf '%s\n' "$1" | grep -Eq "^$2(/|\$)"
}

# 1. Pure packages do no I/O: no outer layers, no web framework, no driver.
for pure in internal/policy internal/model internal/pagekey internal/markup internal/importer internal/spam internal/crypt; do
    [ -d "$pure" ] || continue
    d=$(deps "$pure")
    for bad in store api worker cli config oidc verify mailer idp; do
        if reaches "$d" "$MODULE/internal/$bad"; then fail "$pure depends on internal/$bad"; fi
    done
    for bad in github.com/gin-gonic/gin github.com/jackc/pgx net/http database/sql; do
        if reaches "$d" "$bad"; then fail "$pure depends on $bad"; fi
    done
done

# 2. Only internal/api imports Gin; only internal/store imports pgx; only
# internal/markup produces HTML from readers' text.
direct=$(go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...)
for rule in "github.com/gin-gonic/gin $MODULE/internal/api" "github.com/jackc/pgx $MODULE/internal/store" \
    "github.com/yuin/goldmark $MODULE/internal/markup" "github.com/microcosm-cc/bluemonday $MODULE/internal/markup"; do
    lib=${rule% *}
    owner=${rule#* }
    # The owner's own subpackages, such as internal/store/storetest, count as the owner.
    users=$(printf '%s\n' "$direct" | awk -v lib="$lib" -v owner="$owner" '
        $1 != owner && index($1, owner "/") != 1 { for (i = 2; i <= NF; i++) if (index($i, lib) == 1) { print $1; break } }')
    if [ -n "$users" ]; then fail "only $owner may import $lib, not: $users"; fi
done

# 3. The API and the worker meet only in the database.
if [ -d internal/api ] && reaches "$(deps internal/api)" "$MODULE/internal/worker"; then
    fail "internal/api depends on internal/worker"
fi
if [ -d internal/worker ] && reaches "$(deps internal/worker)" "$MODULE/internal/api"; then
    fail "internal/worker depends on internal/api"
fi

if [ "$status" -eq 0 ]; then
    echo "import boundaries OK"
fi
exit "$status"
