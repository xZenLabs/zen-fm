# Patched current Go toolchain for legacy e-readers

ZenFM's 32-bit ARM packages are built with the supported Go 1.26.5 source
release, not an unsupported Go 1.19/1.20 runtime. The source archive and SHA-256
are pinned in this directory. A narrowly scoped patch routes the Linux/ARM
runtime poller through `epoll_wait`; some supported early Kindle kernels return
`ENOSYS` for the newer `epoll_pwait` syscall before application code can run.

Build the compiler with:

```sh
toolchains/legacy/bootstrap.sh
```

The script verifies the official source archive before applying the checked-in
patch. Set `ZENFM_GO_SOURCE_ARCHIVE` to use an already downloaded archive and
pass an output directory as the first argument when CI needs a different cache
location. Release builds use this compiler only for the ARM soft-float and
hard-float backends.

The patch is intentionally not claimed as an upstream Go configuration. Before
stable release promotion, manually run the old-kernel QEMU and physical Kindle
4/5 and Paperwhite 1 smoke tests described in the release process.
