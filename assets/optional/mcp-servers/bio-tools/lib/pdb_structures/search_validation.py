"""Strict, bounded JSON and numeric primitives for the native PDB searches."""
from __future__ import annotations

import json
import math

MAX_JSON_DEPTH = 64


def object_value(value: object, label: str) -> dict:
    if not isinstance(value, dict):
        raise ValueError(f"Invalid RCSB {label} object")
    return value


def integer_value(value: object, label: str, minimum: int, maximum: int | None = None) -> int:
    if type(value) is not int or value < minimum or maximum is not None and value > maximum:
        raise ValueError(f"Invalid {label} integer")
    return value


def number_value(value: object, label: str, minimum: float = 0,
                 maximum: float | None = None) -> int | float:
    if type(value) not in (int, float):
        raise ValueError(f"Invalid {label} number")
    try:
        finite = math.isfinite(value)
    except OverflowError:
        finite = False
    if not finite or value < minimum or maximum is not None and value > maximum:
        raise ValueError(f"Invalid {label} number")
    return value


def _unique_object(pairs: list[tuple[str, object]]) -> dict:
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate JSON fields")
        result[key] = value
    return result


def _reject_constant(_value: str) -> None:
    raise ValueError("Non-finite JSON number")


def _finite_float(value: str) -> float:
    return float(number_value(float(value), "JSON", -math.inf))


def decode_json(body: bytearray) -> object:
    try:
        depth, in_string, escaped = 0, False, False
        for value in body:
            if in_string:
                if escaped:
                    escaped = False
                elif value == 92:
                    escaped = True
                elif value == 34:
                    in_string = False
            elif value == 34:
                in_string = True
            elif value in (91, 123):
                depth += 1
                if depth > MAX_JSON_DEPTH:
                    raise ValueError("JSON nesting exceeds the decode budget")
            elif value in (93, 125):
                depth -= 1
        return json.loads(body.decode("utf-8"), object_pairs_hook=_unique_object,
                          parse_constant=_reject_constant, parse_float=_finite_float)
    except (UnicodeError, ValueError, RecursionError) as exc:
        raise ValueError("RCSB returned invalid JSON") from exc
