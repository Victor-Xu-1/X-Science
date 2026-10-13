"""Write a genuine scalar HDF5 ordered=false enum into the generated template.

Optional fixture regeneration only; uses Python's standard library and system
HDF5, never a Python AnnData reader. No user data is read or generated.
"""
import ctypes as C
import ctypes.util
import sys

library = ctypes.util.find_library("hdf5_serial")
if not library:
    raise RuntimeError("Fixture regeneration requires the system hdf5_serial library")
hdf5 = C.CDLL(library)
hid = C.c_longlong
for name, args, result in [
    ("H5open", [], C.c_int),
    ("H5Fopen", [C.c_char_p, C.c_uint, hid], hid),
    ("H5Gopen2", [hid, C.c_char_p, hid], hid),
    ("H5Adelete", [hid, C.c_char_p], C.c_int),
    ("H5Tenum_create", [hid], hid),
    ("H5Tenum_insert", [hid, C.c_char_p, C.c_void_p], C.c_int),
    ("H5Screate", [C.c_int], hid),
    ("H5Acreate2", [hid, C.c_char_p, hid, hid, hid, hid], hid),
    ("H5Awrite", [hid, hid, C.c_void_p], C.c_int),
    ("H5Aclose", [hid], C.c_int),
    ("H5Sclose", [hid], C.c_int),
    ("H5Tclose", [hid], C.c_int),
    ("H5Gclose", [hid], C.c_int),
    ("H5Fclose", [hid], C.c_int),
]:
    function = getattr(hdf5, name)
    function.argtypes = args
    function.restype = result


def checked(value, operation):
    if value < 0:
        raise RuntimeError(f"HDF5 fixture operation failed: {operation}")
    return value


checked(hdf5.H5open(), "open library")
file = checked(hdf5.H5Fopen(sys.argv[1].encode(), 1, 0), "open generated template")
group = checked(hdf5.H5Gopen2(file, b"/obs/cell_type", 0), "open categorical group")
checked(hdf5.H5Adelete(group, b"ordered"), "delete temporary uint8 attribute")
base = hid.in_dll(hdf5, "H5T_NATIVE_UINT8_g").value
dtype = checked(hdf5.H5Tenum_create(base), "create boolean enum")
false, true = C.c_ubyte(0), C.c_ubyte(1)
checked(hdf5.H5Tenum_insert(dtype, b"FALSE", C.byref(false)), "insert FALSE")
checked(hdf5.H5Tenum_insert(dtype, b"TRUE", C.byref(true)), "insert TRUE")
space = checked(hdf5.H5Screate(0), "create scalar space")
attribute = checked(hdf5.H5Acreate2(group, b"ordered", dtype, space, 0, 0), "create ordered attribute")
checked(hdf5.H5Awrite(attribute, dtype, C.byref(false)), "write ordered=false")
for name, identifier in [
    ("H5Aclose", attribute),
    ("H5Sclose", space),
    ("H5Tclose", dtype),
    ("H5Gclose", group),
    ("H5Fclose", file),
]:
    checked(getattr(hdf5, name)(identifier), name)
