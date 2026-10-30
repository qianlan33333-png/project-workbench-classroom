#!/usr/bin/env python3
"""Add Linux native packages omitted by a macOS install, pinned by upstream lock."""
import pathlib,urllib.request,hashlib,base64,tarfile,tempfile,shutil,re,json,os
root=pathlib.Path(__file__).resolve().parents[1]
lock=(root/'vendor-src/deepseek-harness/pnpm-lock.yaml').read_text()
packages={'@koromix/koffi-linux-x64':'3.1.1','node-addon-require-builtin-linux-x64-gnu':'0.1.9','@img/sharp-linux-x64':'0.35.5','@img/sharp-libvips-linux-x64':'1.3.4'}
for name,version in packages.items():
    match=re.search(r"^  '?"+re.escape(name+'@'+version)+r"'?:\n    resolution: \{integrity: sha512-([^}]+)\}",lock,re.M)
    assert match,f'{name} version not present in pinned lock'
    expected=match[1]
    archive=root/'.toolchain'/f'{name.replace("@","").replace("/","-")}-{version}.tgz'
    archive.parent.mkdir(exist_ok=True)
    if not archive.exists():
        urllib.request.urlretrieve(f'https://registry.npmjs.org/{name}/-/{name.split("/")[-1]}-{version}.tgz',archive)
    assert base64.b64encode(hashlib.sha512(archive.read_bytes()).digest()).decode()==expected,f'{name} integrity mismatch'
    target=root/'.var/harness-deploy-offline/node_modules'/name
    with tempfile.TemporaryDirectory() as tmp:
        with tarfile.open(archive) as tar:
            for member in tar.getmembers():
                assert member.isfile() or member.isdir(), 'Unsupported archive entry'
                target_path=(pathlib.Path(tmp)/member.name).resolve()
                assert target_path.is_relative_to(pathlib.Path(tmp).resolve()), 'Archive path escaped'
                tar.extract(member,tmp)
        shutil.copytree(pathlib.Path(tmp)/'package',target,dirs_exist_ok=True)
    print(json.dumps({'linux_addon':name,'version':version,'lock_integrity':'verified'}))
modules=root/'.var/harness-deploy-offline/node_modules'
loader=modules/'node-addon-native-custom-loader'
if not loader.exists():loader.symlink_to('.pnpm/node-addon-native-custom-loader@0.1.9/node_modules/node-addon-native-custom-loader')
assert loader.exists(),'Pinned native loader dependency missing'
