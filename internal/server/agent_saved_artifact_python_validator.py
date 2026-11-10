"""Static publication analysis only; artifact source is never executed."""

import ast
import builtins
import json
import symtable
import sys


def definitions(table):
    return {
        symbol.get_name()
        for symbol in table.get_symbols()
        if symbol.is_assigned() or symbol.is_imported()
        or symbol.is_parameter() or symbol.is_namespace()
    }


class ModuleExpressions(ast.NodeVisitor):
    """Visit expressions evaluated by the module, without entering local scopes.

    Deferred global references still come from symtable. They cannot prove when
    a function is called, so a relevant module provider makes them uncertain.
    """

    def visit_FunctionDef(self, node):
        for expression in node.decorator_list + node.args.defaults + node.args.kw_defaults:
            if expression is not None:
                self.visit(expression)
        for argument in node.args.posonlyargs + node.args.args + node.args.kwonlyargs:
            if argument.annotation is not None:
                self.visit(argument.annotation)
        for argument in (node.args.vararg, node.args.kwarg):
            if argument is not None and argument.annotation is not None:
                self.visit(argument.annotation)
        if node.returns is not None:
            self.visit(node.returns)

    visit_AsyncFunctionDef = visit_FunctionDef

    def visit_ClassDef(self, node):
        for expression in node.decorator_list + node.bases:
            self.visit(expression)
        for keyword in node.keywords:
            self.visit(keyword.value)

    def visit_Lambda(self, node):
        for expression in node.args.defaults + node.args.kw_defaults:
            if expression is not None:
                self.visit(expression)

    def visit_ListComp(self, node):
        # Only the outer iterable belongs to the containing scope.
        self.visit(node.generators[0].iter)

    visit_SetComp = visit_ListComp
    visit_DictComp = visit_ListComp
    visit_GeneratorExp = visit_ListComp


def builtin_call(node, name, module_definitions):
    return (isinstance(node, ast.Call) and isinstance(node.func, ast.Name)
            and node.func.id == name and name not in module_definitions)


def module_globals(node, module_definitions):
    return (builtin_call(node, "globals", module_definitions)
            and not node.args and not node.keywords)


def literal_bindings(code, module_definitions):
    """A literal can only excuse names it might bind, never unrelated names.

    None means an opaque provider; a set is a closed possible binding set.
    Invalid literal code cannot provide names. No compile/exec of it occurs.
    """
    if (builtin_call(code, "compile", module_definitions) and len(code.args) >= 3
            and isinstance(code.args[2], ast.Constant) and code.args[2].value == "exec"):
        code = code.args[0]
    if not isinstance(code, ast.Constant) or not isinstance(code.value, (str, bytes)):
        return None
    try:
        tree = ast.parse(code.value, "dynamic-code", "exec")
        table = symtable.symtable(code.value, "dynamic-code", "exec")
    except (SyntaxError, ValueError):
        return set()
    # Nested exec, wildcard import and explicit namespace mutation prevent a
    # closed literal binding proof. Local function bodies are excluded.
    class OpenNamespace(ModuleExpressions):
        opaque = False

        def visit_Call(self, node):
            if (isinstance(node.func, ast.Name) and node.func.id in ("exec", "globals", "locals", "vars")):
                self.opaque = True
            self.generic_visit(node)

        def visit_ImportFrom(self, node):
            if any(alias.name == "*" for alias in node.names):
                self.opaque = True

    namespace = OpenNamespace()
    namespace.visit(tree)
    return None if namespace.opaque else definitions(table)


class ModuleNamespace(ModuleExpressions):
    def __init__(self, module_definitions, unresolved):
        self.module_definitions = module_definitions
        self.unresolved = unresolved
        self.providers = []
        self.loads = {name: [] for name in unresolved}

    def visit_Name(self, node):
        if isinstance(node.ctx, ast.Load) and node.id in self.unresolved:
            self.loads[node.id].append((node.lineno, node.col_offset))

    def visit_Call(self, node):
        # A separate locals dictionary does not populate ordinary module names.
        # Builtin shadowing prevents this narrow module-namespace recognition.
        if (builtin_call(node, "exec", self.module_definitions) and 1 <= len(node.args) <= 3
                and not node.keywords
                and (len(node.args) == 1 or module_globals(node.args[1], self.module_definitions))
                and (len(node.args) < 3 or module_globals(node.args[2], self.module_definitions))):
            self.providers.append(((node.end_lineno, node.end_col_offset),
                                   literal_bindings(node.args[0], self.module_definitions)))
        self.generic_visit(node)

    def classify(self):
        definite, indeterminate = set(), set()
        for name in self.unresolved:
            relevant = [position for position, bindings in self.providers
                        if bindings is None or name in bindings]
            # A module load before every possible provider is still definitely
            # missing. Unknown findings cannot erase a definite one.
            if not relevant or any(not any(provider <= load for provider in relevant)
                                   for load in self.loads[name]):
                definite.add(name)
            else:
                indeterminate.add(name)
        return definite, indeterminate


def analyze(source):
    result = {"ok": False, "code": "syntax_error", "unresolved": [], "unresolved_count": 0,
              "indeterminate": [], "indeterminate_count": 0}
    try:
        root = symtable.symtable(source, "artifact.py", "exec")
        tree = ast.parse(source, "artifact.py", "exec")
    except (SyntaxError, ValueError):
        return result
    module_definitions = definitions(root)
    allowed = set(dir(builtins)) | {"__name__", "__file__", "__package__", "__spec__"}
    unresolved = set()

    def visit(table):
        for symbol in table.get_symbols():
            name = symbol.get_name()
            if symbol.is_referenced() and symbol.is_global() and name not in module_definitions and name not in allowed:
                unresolved.add(name)
        for child in table.get_children():
            visit(child)

    visit(root)
    namespace = ModuleNamespace(module_definitions, unresolved)
    namespace.visit(tree)
    definite, indeterminate = namespace.classify()
    result.update(unresolved=sorted(definite)[:32], unresolved_count=len(definite),
                  indeterminate=sorted(indeterminate)[:32], indeterminate_count=len(indeterminate))
    if definite:
        result["code"] = "unresolved_global"
    elif indeterminate:
        result["code"] = "analysis_indeterminate"
    else:
        result.update(ok=True, code="valid_python")
    return result


print(json.dumps(analyze(sys.stdin.read()), sort_keys=True))
