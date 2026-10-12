"""Lexical identity-literal precision for current namespace references.

This module grants only narrow import, path, protocol and executable contexts.
The calling gate remains the sole product/version and release authority.
"""
from __future__ import annotations

import ast
import re

_GO_TOKEN = re.compile(r'"(?:\\.|[^"\\])*"|`[^`]*`|\'(?:\\.|[^\'\\])*\'|[A-Za-z_][A-Za-z_0-9]*|:=|\n|[^\s]', re.DOTALL)
_IDENTITY_SLOT = {"machineslug", "machine_slug", "productslug", "product_slug", "displayname", "display_name"}
_GO_KEYWORDS = set("break default func interface select case defer go map struct chan else goto package switch const fallthrough if range type continue for import return var".split())


def _string_value(token: str) -> str | None:
    if token.startswith("`") and token.endswith("`"):
        return token[1:-1]
    if token.startswith('"') and token.endswith('"'):
        try:
            value = ast.literal_eval(token)
            return value if isinstance(value, str) else None
        except (SyntaxError, ValueError):
            return None
    return None


def _go_import_tokens(tokens: list[str]) -> tuple[set[int], dict[str, str]]:
    """Recognize only actual package-prefix import specs, never quoted code."""
    index = 0
    imports, aliases = set(), {}
    while index < len(tokens) and tokens[index] == "\n":
        index += 1
    if tokens[index:index + 1] != ["package"] or index + 2 >= len(tokens):
        return imports, aliases
    if (not re.fullmatch(r"[A-Za-z_][A-Za-z_0-9]*", tokens[index + 1])
            or tokens[index + 1] in _GO_KEYWORDS or tokens[index + 2] not in {"\n", ";"}):
        return imports, aliases
    index += 2
    while index < len(tokens):
        while index < len(tokens) and tokens[index] in {"\n", ";"}:
            index += 1
        if tokens[index:index + 1] != ["import"]:
            break
        index += 1
        block = tokens[index:index + 1] == ["("]
        index += int(block)
        closed = not block
        accepted, named = set(), {}
        while index < len(tokens):
            if block:
                while index < len(tokens) and tokens[index] in {"\n", ";"}:
                    index += 1
                if tokens[index:index + 1] == [")"]:
                    index += 1
                    closed = True
                    break
            alias = None
            if index < len(tokens) and (tokens[index] == "." or re.fullmatch(r"[A-Za-z_][A-Za-z_0-9]*", tokens[index])):
                alias = tokens[index]
                if alias in _GO_KEYWORDS:
                    return imports, aliases
                index += 1
            value = _string_value(tokens[index]) if index < len(tokens) else None
            if value is None:
                return imports, aliases
            accepted.add(index)
            if value in {"path/filepath", "errors", "fmt"} and alias not in {".", "_"}:
                named[value] = alias or value.rsplit("/", 1)[-1]
            index += 1
            if not block:
                break
            if index >= len(tokens) or tokens[index] not in {"\n", ";", ")"}:
                return imports, aliases
        if not closed or index < len(tokens) and tokens[index] not in {"\n", ";"}:
            return imports, aliases
        imports.update(accepted)
        aliases.update(named)
    return imports, aliases


def _namespace_token_present(text: str, value: str) -> bool:
    # A schema, API, CSS, service or path-qualified token is not a new identity.
    return re.search(r"(?<![A-Za-z0-9_.:/\\-])" + re.escape(value) + r"(?![A-Za-z0-9_.:/\\-])", text) is not None


def _go_identity_literal_present(text: str, derived: dict[str, str]) -> bool:
    matches = list(_GO_TOKEN.finditer(text))
    tokens = [match.group() for match in matches]
    imports, aliases = _go_import_tokens(tokens)
    slug, display = derived["machine_slug"], derived["display_name"]
    # A package can be locally shadowed. Only unshadowed stdlib selector use
    # establishes the path/error-call contexts below.
    aliases = {path: alias for path, alias in aliases.items() if all(
        index in imports or tokens[index + 1:index + 2] == ["."]
        or index + 1 in imports for index, token in enumerate(tokens) if token == alias)}
    allowed = {index for index in imports if _string_value(tokens[index]) == slug}
    # A typed identity field/declaration is never a schema or binary-context
    # exception, even when its initializer builds a qualified-looking string.
    for index, token in enumerate(tokens):
        if token.strip('"`').lower() not in _IDENTITY_SLOT or tokens[index + 1:index + 2] not in ([":"], ["="], [":="]):
            continue
        depth, end = 0, index + 2
        while end < len(tokens):
            current = tokens[end]
            if depth == 0 and current in {",", ";", "\n", "}"}:
                break
            depth += current in {"(", "[", "{"}
            depth -= current in {")", "]", "}"}
            value = _string_value(current)
            if value is not None and (slug in value or display in value):
                return True
            end += 1
    for index, token in enumerate(tokens):
        value = _string_value(token)
        if value is None:
            continue
        if value == slug and tokens[index - 2:index] == ['"command"', ":"] and tokens[index + 1:index + 7] == [",", '"args"', ":", "[", "]", "string"]:
            allowed.add(index)
    # Qualified asset-root joins have a provably nonempty literal component.
    for index, token in enumerate(tokens):
        if token != aliases.get("path/filepath") or tokens[index + 1:index + 4] != [".", "Join", "("]:
            continue
        end, depth = index + 4, 1
        while end < len(tokens) and depth:
            depth += (tokens[end] == "(") - (tokens[end] == ")")
            end += 1
        arguments = tokens[index + 4:end - 1]
        pieces, start, nested = [], 0, 0
        for position, item in enumerate(arguments):
            if item == "," and nested == 0:
                pieces.append(arguments[start:position])
                start = position + 1
            nested += item in {"(", "[", "{"}
            nested -= item in {")", "]", "}"}
        pieces.append(arguments[start:])
        stable_root = any(
            len(piece) == 1 and _string_value(piece[0]) in {"assets", "skills"}
            and all(len(tail) == 1 and (value := _string_value(tail[0])) not in {None, "", ".", ".."}
                    and "/" not in value and "\\" not in value for tail in pieces[ordinal + 1:])
            for ordinal, piece in enumerate(pieces)
        )
        if stable_root:
            allowed.update(position for position in range(index + 4, end - 1) if _string_value(tokens[position]) == slug)
    for index, token in enumerate(tokens):
        value = _string_value(token)
        if value is None:
            continue
        for path, method in (("errors", "New"), ("fmt", "Errorf")):
            if (tokens[index - 4:index] == [aliases.get(path), ".", method, "("]
                    and re.match(r"^usage:\s+" + re.escape(slug) + r"\s+[a-z]", value, re.IGNORECASE)
                    and value.count(slug) == 1):
                allowed.add(index)
        if _namespace_token_present(value, display) or index not in allowed and _namespace_token_present(value, slug):
            return True
    # Malformed/unquoted source is never granted an import exception.
    outside = list(text)
    for match in matches:
        if _string_value(match.group()) is not None:
            outside[match.start():match.end()] = " " * (match.end() - match.start())
    return _namespace_token_present("".join(outside), slug) or _namespace_token_present("".join(outside), display)


def _text_identity_literal_present(text: str, derived: dict[str, str]) -> bool:
    slug, display = derived["machine_slug"], derived["display_name"]
    for match in re.finditer(r"(?i)\b(machine_?slug|product_?slug|display_?name)\b\s*[:=]\s*([\"'])([^\n]*?)\2", text):
        if slug in match[3] or display in match[3]:
            return True
    # Exact executable lookup loop: the bare basename is a filesystem choice,
    # not a second machine-slug authority. No path/file whitelist is used.
    executable_loop = re.compile(r"foreach\s*\(\$(\w+)\s+in\s+@\('" + re.escape(slug) + r"\.exe',\s*'" + re.escape(slug) + r"'\)\)\s*\{\s*\$\w+\s*=\s*Join-Path\s+\$\w+\s+\$\1")
    text = executable_loop.sub(lambda match: match.group() if match[1].lower() in _IDENTITY_SLOT else match.group().replace("'" + slug + "'", "'EXECUTABLE_BASENAME'"), text)
    # The shell's executable-error description is typed by its echo command.
    text = re.sub(r'(?m)^([ \t]*echo[ \t]+"[^"\n]*)\(' + re.escape(slug) + r' binary\)', r'\1(EXECUTABLE_BASENAME binary)', text)
    text = re.sub(r'--binaries\s+"' + re.escape(slug) + r'\$\{binary_suffix\},' + re.escape(slug) + r'-live-im-smoke\$\{binary_suffix\}"', '--binaries "DERIVED_EXECUTABLE_SET"', text)
    return _namespace_token_present(text, slug) or _namespace_token_present(text, display)
