#!/usr/bin/env python3
"""Close pnpm's link: overrides over a fixed, already built upstream checkout.

pnpm deploy --legacy retains link: overrides and unused workspace aliases.
Only two shipped vendor libraries and platform addon packages are external
runtime dependencies; unexpected production links fail instead of guessing.
"""
import pathlib,sys,shutil,os,json,hashlib
root=pathlib.Path(__file__).resolve().parents[1]
dest=(root/(sys.argv[1] if len(sys.argv)>1 else '.var/harness-deploy-offline')).resolve()
upstream=(root/'vendor-src/deepseek-harness').resolve()
relocated=removed=0
for link in list(dest.rglob('*')):
    if not link.is_symlink(): continue
    source=link.resolve()
    if source.is_relative_to(dest): continue
    rel=source.relative_to(upstream)
    if rel.as_posix() in ('vendor/cosmokit','vendor/schemastery'):
        target=dest/rel
    elif rel.parent.as_posix()=='native/system/packages' and rel.name!='entry':
        target=dest/'native-packages'/rel.name
    elif link.relative_to(dest).as_posix().startswith('node_modules/.pnpm/node_modules/'):
        link.unlink();removed+=1;continue
    else:
        raise RuntimeError(f'Unexpected external production dependency: {link} -> {source}')
    if not target.exists():
        shutil.copytree(source,target,symlinks=True,ignore=shutil.ignore_patterns('node_modules','.git'))
    link.unlink();link.symlink_to(os.path.relpath(target,link.parent));relocated+=1
# The pinned upstream app also omits several non-optional workspace peers from
# its deploy graph. Resolve them from the same checked-out commit, never npm latest.
workspace={}
for parent,dirs,files in os.walk(upstream):
    dirs[:]=[d for d in dirs if d not in ('node_modules','.git','.cache')]
    if 'package.json' in files:
        p=pathlib.Path(parent);meta=json.loads((p/'package.json').read_text())
        if meta.get('name'):workspace[meta['name']]=p
def resolved(name,base):
    for p in [base,*base.parents]:
        if not p.is_relative_to(dest):break
        if (p/'node_modules'/name/'package.json').exists():return True
    return False
copied=[]
for _ in range(12):
    added=False
    for manifest in list(dest.rglob('package.json')):
        meta=json.loads(manifest.read_text())
        if meta.get('name') not in workspace:continue
        dependencies=dict(meta.get('dependencies',{}))
        dependencies.update({k:v for k,v in meta.get('peerDependencies',{}).items() if not meta.get('peerDependenciesMeta',{}).get(k,{}).get('optional')})
        for name in dependencies:
            if name not in workspace or resolved(name,manifest.parent):continue
            source=workspace[name];target=dest/'workspace-packages'/name.replace('/','_')
            if not target.exists():shutil.copytree(source,target,symlinks=True,ignore=shutil.ignore_patterns('node_modules','.git'))
            link=dest/'node_modules'/name;link.parent.mkdir(parents=True,exist_ok=True)
            if link.is_symlink():link.unlink()
            link.symlink_to(os.path.relpath(target,link.parent));added=True;copied.append(name)
    if not added:break
else:raise RuntimeError('Unclosed workspace peer graph')
for link in dest.rglob('*'):
    if link.is_symlink():
        assert link.resolve().is_relative_to(dest),f'External link: {link}'
        assert link.exists(),f'Broken link: {link}'
print(json.dumps({'relocated_links':relocated,'unused_workspace_aliases_removed':removed,'fixed_commit_workspace_peers':copied}))
