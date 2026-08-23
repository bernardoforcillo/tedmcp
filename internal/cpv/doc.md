# The embedded CPV vocabulary

`cpv.json.gz` is the Common Procurement Vocabulary — Commission Regulation (EC)
No 213/2008 — as 9,454 codes with their names in the 24 official EU languages
and their level in the hierarchy. 2.4 MB compressed, decompressed on first use
and not before.

It is embedded rather than fetched. The whole point of the package is to stop a
search failing silently for want of the right code, and a tool with that job
should not itself be able to fail because a host was unreachable.

## Where it comes from

The data was taken from [`cpv-eu`](https://www.npmjs.com/package/cpv-eu) (MIT),
which publishes the vocabulary as JSON. The vocabulary itself is EU legislation.

## Regenerating it

Only needed if the Commission revises the CPV, which it has not done since 2008.

```sh
npm pack cpv-eu && tar xzf cpv-eu-*.tgz
python3 - <<'PY'
import json, gzip
d = json.load(open('package/data/cpv.json'))
out = [{"code": r["code"], "level": r["level"], "labels": r["labels"]}
       for r in sorted(d, key=lambda r: r["code"])]
blob = json.dumps(out, ensure_ascii=False, separators=(',', ':')).encode()
with gzip.GzipFile('internal/cpv/cpv.json.gz', 'wb', compresslevel=9, mtime=0) as f:
    f.write(blob)
PY
```

`mtime=0` keeps the output byte-identical between runs, so regenerating without
a data change produces no diff. The `emoji` field the source carries is dropped:
nothing here reads it.
