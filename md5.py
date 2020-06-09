import hashlib, sys

def md5(fname):
    hash_md5 = hashlib.md5()
    with open(sys.argv[1], "rb") as f:
        for chunk in iter(lambda: f.read(4096), b""):
            hash_md5.update(chunk)
    return hash_md5.hexdigest()

if len(sys.argv) != 1:
    exit

print(md5(sys.argv[1]))