"""Archive all experiment materials, public inputs and Git history locally.
Requires a clean experiment Git tree and an otherwise empty user-authorized target.
Does not stage/commit/push anything in the parent product repository.
"""
import hashlib,json,shutil,subprocess,datetime
from pathlib import Path
R=Path(__file__).resolve().parents[1]
D=Path('/Users/songyy/Documents/event-driven-context/docs/research/2026-09-12-memory-lab')
def sha(p):
    h=hashlib.sha256()
    with p.open('rb') as f:
        for b in iter(lambda:f.read(1024*1024),b''):h.update(b)
    return h.hexdigest()
def main():
    assert (R/'results/main-v2/completed.json').exists()
    assert json.loads((R/'results/runtime-cleanup.json').read_text())['port_closed']
    assert subprocess.check_output(['git','status','--porcelain'],cwd=R,text=True)==''
    assert D.is_dir() and (D/'ARCHIVE-STATUS.md').exists(), 'User-authorized archive marker required'
    assert not [p for p in D.iterdir() if p.name!='ARCHIVE-STATUS.md'], 'Target already contains files; do not overwrite an existing archive'
    excluded={'.git','.venv','.runtime','__pycache__'};copied=[]
    for p in R.rglob('*'):
        rel=p.relative_to(R)
        if any(x in excluded for x in rel.parts) or not p.is_file():continue
        assert not p.is_symlink(), 'Do not follow unknown symlinks into archive'
        target=D/rel;target.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(p,target)
        assert sha(p)==sha(target);copied.append(str(rel))
    commit=subprocess.check_output(['git','rev-parse','HEAD'],cwd=R,text=True).strip()
    subprocess.run(['git','bundle','create',str(D/'experiment-history.bundle'),'--all'],cwd=R,check=True)
    subprocess.run(['git','bundle','verify',str(D/'experiment-history.bundle')],cwd=R,check=True,capture_output=True)
    now=datetime.datetime.now(datetime.timezone.utc).isoformat()
    meta={'archived_at':now,'source_directory':str(R),'destination':str(D),'experiment_commit':commit,'copied_files':len(copied),'copied_files_sha256_match':True,'git_bundle_verified':True,'excluded':['rebuildable .venv','transient .runtime (server log copied under results/runtime)','Python __pycache__','live .git (complete committed history in experiment-history.bundle)'],'public_upstream_included':True,'parent_repository_index_changed':False,'note':'All experiment process logs/results are local. Archive is a new product docs directory authorized by the user; no production or product code changes.'}
    (D/'archive.json').write_text(json.dumps(meta,ensure_ascii=False,indent=2))
    (D/'ARCHIVE-STATUS.md').write_text(f'# 实验归档已完成\n\n归档时间：{now}\n\n入口：[研究报告](REPORT.md)、[复现与目录索引](README.md)、[执行记录](results/execution.md)、[过程日志索引](results/process-notes.md)、[完整结果](results/summary.md)。\n\n独立实验提交：`{commit}`。完整历史在 `experiment-history.bundle`，可用 `git clone experiment-history.bundle <新目录>` 查看；公开输入文件在 `data/upstream/`，也保留了下载脚本和内容哈希。\n\n复制的 {len(copied)} 个文件逐一与源文件比对 SHA-256；归档内全部文件的完整性清单为 `MANIFEST.sha256`，可在本目录运行 `shasum -a 256 -c MANIFEST.sha256` 检查。\n\n这是完整实验归档；没有把父产品仓库的并行改动加入实验提交，也没有对父仓库暂存、提交或推送。可重建虚拟环境和已有模型权重未复制。\n')
    manifest=[]
    for p in sorted(D.rglob('*')):
        if p.is_file() and p.name!='MANIFEST.sha256':manifest.append(f'{sha(p)}  {p.relative_to(D)}')
    (D/'MANIFEST.sha256').write_text('\n'.join(manifest)+'\n')
    for line in manifest:
        expected,path=line.split('  ',1);assert sha(D/path)==expected
    print(json.dumps(dict(meta,manifest_files=len(manifest),bytes=sum(p.stat().st_size for p in D.rglob('*') if p.is_file())),ensure_ascii=False,indent=2))
if __name__=='__main__':main()
