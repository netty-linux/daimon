import os, stat, json
# Fixed inspection only: no file-content reads and no transfer.
root = '/workspace'
try:
    rootfd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    rootstat = os.fstat(rootfd)
    result = []
    files = dirs = total = 0
    identities = set()
    def identity(s):
        return (s.st_dev,s.st_ino,s.st_mode,s.st_nlink,s.st_size,s.st_mtime_ns,s.st_ctime_ns)
    def walk(parentfd, prefix, depth):
        global files, dirs, total
        if depth > 8:
            raise ValueError()
        names = []
        with os.scandir(parentfd) as entries:
            for entry in entries:
                names.append(entry.name)
                if len(names) > 96:
                    raise ValueError()
        for name in sorted(names):
            relative = prefix + name
            if len(relative.encode('utf-8')) > 4096:
                raise ValueError()
            s = os.stat(name, dir_fd=parentfd, follow_symlinks=False)
            directory = stat.S_ISDIR(s.st_mode)
            if s.st_dev != rootstat.st_dev:
                raise ValueError()
            if not directory and (not stat.S_ISREG(s.st_mode) or s.st_nlink != 1):
                raise ValueError()
            if s.st_mode & (stat.S_ISUID | stat.S_ISGID | stat.S_ISVTX):
                raise ValueError()
            if directory:
                dirs += 1
            else:
                key = (s.st_dev,s.st_ino)
                if key in identities:
                    raise ValueError()
                identities.add(key)
                files += 1
                total += s.st_size
            if files > 64 or dirs > 32 or total > 1048576 or (not directory and s.st_size > 65536):
                raise ValueError()
            result.append({'path':relative,'directory':directory,'size':0 if directory else s.st_size,'inode':s.st_ino,'device':s.st_dev,'links':s.st_nlink,'mtime':s.st_mtime_ns,'ctime':s.st_ctime_ns})
            if directory:
                childfd = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parentfd)
                try:
                    if identity(os.fstat(childfd)) != identity(s):
                        raise ValueError()
                    walk(childfd, relative+'/', depth+1)
                finally:
                    os.close(childfd)
            if identity(os.stat(name,dir_fd=parentfd,follow_symlinks=False)) != identity(s):
                raise ValueError()
    try:
        walk(rootfd,'',0)
        if identity(rootstat) != identity(os.fstat(rootfd)) or identity(rootstat) != identity(os.lstat(root)):
            raise ValueError()
    finally:
        os.close(rootfd)
    encoded = json.dumps({'version':1,'entries':result},ensure_ascii=True,separators=(',',':'))
    if len(encoded) > 65536:
        raise ValueError()
    print(encoded)
except Exception:
    raise SystemExit(1)
