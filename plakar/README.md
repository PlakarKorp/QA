# plakar

The fuzz targets OSS-Fuzz runs against plakar, with the files `projects/plakar`
in google/oss-fuzz holds to build them. See [fuzz/README.md](fuzz/README.md).

Needs the plakar checkout beside QA: `go.mod` replaces
`github.com/PlakarKorp/plakar` with `../../plakar`, so the targets always
fuzz the tree you have, and the OSS-Fuzz builder clones both repositories
into `$SRC` to get the same layout.

    make qa                          # the seeds, as regression tests
    make fuzz TARGET=FuzzPackfileCodec FUZZTIME=5m
