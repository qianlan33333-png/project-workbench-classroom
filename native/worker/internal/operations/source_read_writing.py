"""Read approved writing assets; never create original tasks or invoke a model."""
import json, sys
from pathlib import Path
root=Path('/opt/daru-v3-warehouse-candidate')
sys.path.insert(0,str(root/'ops'))
from prompt_runtime import PromptRuntime
runtime=PromptRuntime()
files=['var/newcomer-mini-program-entry.json',
       'prompts/runtime/mirror-newcomer-sop-reference-v1.json',
       'docs/product-operation-packs/shared/owner-service-copy.v1.json',
       'docs/product-operation-packs/shared/returning-registered-invitation.v1.json']
reference=runtime.manifest.get('newcomer_service_reference')
if reference:
    if Path(reference).name!=reference:raise ValueError('invalid_original_reference')
    files.append('prompts/runtime/'+reference)
assets={name:json.loads((root/name).read_text())for name in set(files)if(root/name).is_file()}
if reference:assets['prompts/runtime/'+reference]=runtime._resource(reference)
print(json.dumps({'assets':assets,'newcomer_reference':reference,'read_only':True},ensure_ascii=False))
