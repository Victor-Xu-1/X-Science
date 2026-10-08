"""The product's per-PR numeric counter, independent of API/dependency SemVer."""

from dataclasses import dataclass
import re

MINOR_RADIX = 10
PATCH_RADIX = 100
VERSION = re.compile(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)")


@dataclass(frozen=True, order=True)
class ProductVersion:
    major: int
    minor: int
    patch: int

    def __post_init__(self) -> None:
        if (any(type(value) is not int for value in (self.major, self.minor, self.patch))
                or self.major < 0 or not 0 <= self.minor < MINOR_RADIX
                or not 0 <= self.patch < PATCH_RADIX):
            raise ValueError("invalid product counter components")

    @classmethod
    def parse(cls, value: str) -> "ProductVersion":
        match = VERSION.fullmatch(value) if type(value) is str else None
        if match is None:
            raise ValueError("invalid canonical product version")
        return cls(*(int(part) for part in match.groups()))

    def advance(self) -> "ProductVersion":
        minor_carry, patch = divmod(self.patch + 1, PATCH_RADIX)
        major_carry, minor = divmod(self.minor + minor_carry, MINOR_RADIX)
        return ProductVersion(self.major + major_carry, minor, patch)

    def __str__(self) -> str:
        return f"{self.major}.{self.minor}.{self.patch}"


def next_version(current: str) -> str:
    return str(ProductVersion.parse(current).advance())


def require_next_version(previous: str, candidate: str) -> None:
    if ProductVersion.parse(candidate) != ProductVersion.parse(previous).advance():
        raise ValueError("version must advance one counter step")
