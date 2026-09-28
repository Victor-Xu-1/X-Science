"""Propagate discarded synchronous process failures in submitted Python cells.

Only expression statements discard their result. Assigned, returned, conditional
and otherwise consumed statuses retain native Python semantics, including caller
fallbacks. Library code is not rewritten and process APIs are not monkeypatched.
This is outcome propagation, not a security boundary or a subprocess supervisor.
"""
import ast
import os
import subprocess
import types


_SYSTEM = os.system
_RUN = subprocess.run
_CALL = subprocess.call
_WAIT = subprocess.Popen.wait
_COMMUNICATE = subprocess.Popen.communicate


def checked_process_call(target):
    # Return ordinary callables unchanged: moving exec(), locals() or super()
    # into a helper frame would silently change otherwise valid Python.
    if target is _SYSTEM:
        kind = "system"
    elif target is _RUN:
        kind = "run"
    elif target is _CALL:
        kind = "call"
    elif type(target) is types.MethodType and target.__func__ is _WAIT:
        kind = "wait"
    elif type(target) is types.MethodType and target.__func__ is _COMMUNICATE:
        kind = "communicate"
    else:
        return target

    def execute(*args, **kwargs):
        result = target(*args, **kwargs)
        stdout = stderr = None
        if kind == "system":
            returncode = os.waitstatus_to_exitcode(result) if os.name == "posix" else result
            command = args[0] if args else kwargs.get("command")
        elif kind == "run":
            returncode, command = result.returncode, result.args
            stdout, stderr = result.stdout, result.stderr
        elif kind == "call":
            returncode = result
            command = args[0] if args else kwargs.get("args")
        else:
            process = target.__self__
            returncode, command = process.returncode, process.args
            if kind == "communicate":
                stdout, stderr = result
        if returncode:
            raise subprocess.CalledProcessError(returncode, command, output=stdout, stderr=stderr)
        return result

    return execute


class _DiscardedProcessCalls(ast.NodeTransformer):
    def __init__(self, helper_name):
        self.helper_name = helper_name
        self.changed = False

    def visit_Expr(self, node):
        self.generic_visit(node)
        if isinstance(node.value, ast.Call):
            call = node.value
            # Resolve the original callable once and keep argument evaluation
            # in the caller, in the same order. No command is replayed.
            call.func = ast.copy_location(ast.Call(
                func=ast.Name(id=self.helper_name, ctx=ast.Load()),
                args=[call.func], keywords=[],
            ), call.func)
            self.changed = True
        return node


def prepare_process_outcomes(tree, namespace):
    # A persistent binding supports functions defined in earlier cells. Never
    # overwrite user bindings, including names introduced by this source.
    identifiers = set()
    for node in ast.walk(tree):
        for field, value in ast.iter_fields(node):
            if field in ("id", "arg", "name", "asname") and isinstance(value, str):
                identifiers.add(value)
            elif field == "names" and isinstance(node, (ast.Global, ast.Nonlocal)):
                identifiers.update(value)
    base, index = "_synon_checked_process_call", 0
    name = base
    while name in identifiers or (name in namespace and namespace[name] is not checked_process_call):
        index += 1
        name = base + "_" + str(index)
    transformer = _DiscardedProcessCalls(name)
    tree = transformer.visit(tree)
    if transformer.changed:
        namespace[name] = checked_process_call
        ast.fix_missing_locations(tree)
    return tree
