"""Fetch only publicly released benchmark files and verify exact content hashes."""
import json,hashlib,urllib.request
from pathlib import Path
R=Path(__file__).resolve().parents[1]
for item in json.loads((R/'sources/downloads.json').read_text()):
    p=R/'data/upstream'/item['file'];p.parent.mkdir(parents=True,exist_ok=True)
    url=item['url'].replace('snap-research/locomo/main/','snap-research/locomo/3eb6f2c585f5e1699204e3c3bdf7adc5c28cb376/')
    if not p.exists():
        print('download',url,flush=True);p.write_bytes(urllib.request.urlopen(url,timeout=240).read())
    actual=hashlib.sha256(p.read_bytes()).hexdigest()
    if actual!=item['sha256']:raise RuntimeError(f'Checksum mismatch: {p.name}; do not silently change the benchmark revision.')
    print('verified',p.name,actual)
