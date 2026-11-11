"""Read original outbound writing resources only, with no model or sending."""
import sys,json
sys.path.insert(0,'/opt/daru-v3-warehouse-candidate/ops')
from prompt_runtime import PromptRuntime
runtime=PromptRuntime();result={}
for skill in ['personal_followup','new_user_onboarding','weekly_review','content_service','activation_recall','sales_assist']:
    result[skill]=runtime._resources_for(skill,{'skill_key':skill})
json.dump(result,sys.stdout,ensure_ascii=False)
