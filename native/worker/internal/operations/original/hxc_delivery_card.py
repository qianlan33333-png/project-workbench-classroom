"""Original deterministic functions; sources are bound by the adapter."""
import re
from urllib.parse import urlsplit, parse_qs

APPID = 'wx0ca836834b18e989'

def descriptor(request):
    scene=request.get('scene')
    material=request.get('review_material') if scene=='weekly_review' else request.get('content_material')
    material=material or {}
    if not material:
        if scene in {'weekly_review','content_service'}: raise ValueError('delivery_material_missing')
        return None
    typ='review' if scene=='weekly_review' else str(material.get('content_type') or request.get('content_type') or '')
    cid=str(material.get('review_id') or material.get('published_detail_review_id') or request.get('review_id') or '') if typ=='review' else str(material.get('content_id') or request.get('content_id') or '')
    page=str(material.get('base_target_path') or '')
    split=urlsplit(page)
    if typ=='link' and material.get('purpose')=='growth_report':
        pin=material.get('report_ref') or {}
        if (pin.get('report_ref')!=cid or type(pin.get('version')) is not int
                or pin.get('version')!=material.get('report_version')
                or pin.get('content_digest')!=material.get('content_digest')):
            raise ValueError('delivery_report_version_missing')
        queries=parse_qs(split.query,keep_blank_values=True)
        if (split.scheme or split.netloc or split.fragment or split.path!='/pages/outcome/outcome'
                or set(queries)!={'id'} or len(queries['id'])!=1
                or not re.fullmatch(r'[A-Za-z0-9_-]{1,64}',queries['id'][0])):
            raise ValueError('delivery_report_page_invalid')
        return {'type':'mini_program','appid':APPID,'page':page,'title':'你的综合评估报告',
                'content_type':'link','content_id':cid,'material_version':pin['version'],
                'report_ref':pin,'source_ref':material.get('source_ref'),'purpose':'growth_report',
                'verified':False,'status':'awaiting_final_review_and_scrm_library'}
    if not cid or not re.fullmatch(r'[A-Za-z0-9_-]{1,64}',cid) or typ not in {'lesson','observation','case','review'}:
        raise ValueError('delivery_identity_or_type_unavailable')
    if split.scheme or split.netloc or not split.path.lstrip('/').startswith('pages/') or 'dt' in parse_qs(split.query):
        raise ValueError('delivery_page_invalid')
    queries=parse_qs(split.query)
    routes={'lesson':('pages/article/article','lesson_id'),'observation':('pages/observation-issue/observation-issue','id'),'case':('pages/case-detail/case-detail','id'),'review':('pages/observation/observation','rid')}
    route,key=routes[typ]
    if split.path.lstrip('/')!=route or queries.get(key)!=[cid] or (typ=='review' and queries.get('kind')!=['review']):
        raise ValueError('delivery_page_identity_mismatch')
    if typ=='review' and not material.get('published_detail_version'):
        raise ValueError('delivery_review_version_missing')
    title='给你的每周学习总结' if typ=='review' else str(material.get('title') or request.get('content_title') or '')
    if not title: raise ValueError('delivery_title_missing')
    while len(title.encode())>64:title=title[:-1]
    return {'type':'mini_program','appid':APPID,'page':page,'title':title,
            'content_type':typ,'content_id':cid,'material_version':material.get('published_detail_version') if typ=='review' else material.get('body_catalog_version'),
            'source_ref':material.get('source_ref'),'purpose':'个人学习总结' if typ=='review' else '推荐内容全文',
            'verified':False,'status':'awaiting_final_review_and_scrm_library',
            'note':'审核时核对本人权限、内容与页面；正式交接前复用或登记 SCRM 小程序素材并核验。素材未就绪不能发送；不额外签发 dt 路径。'}
