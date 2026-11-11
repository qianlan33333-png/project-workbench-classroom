"""Original deterministic functions; sources are bound by the adapter."""
import re
import unicodedata

SURNAMES=set('赵钱孙李周吴郑王冯陈褚卫蒋沈韩杨朱秦尤许何吕施张孔曹严华金魏陶姜戚谢邹喻柏水窦章云苏潘葛范彭郎鲁韦马苗方俞任袁柳鲍史唐费廉岑薛雷贺倪汤滕殷罗毕郝邬安常乐于傅皮齐康伍余元卜顾孟平黄和穆萧尹姚邵汪毛禹狄米贝明臧计伏成戴宋茅庞熊纪舒屈项祝董梁杜阮蓝闵席季麻强贾路娄危江童颜郭梅盛林刁钟徐邱骆高夏蔡田樊胡凌霍虞万卢莫房解应宗丁宣邓单杭洪包左石崔龚程嵇邢裴陆荣翁荀羊惠甄曲封储靳段富焦侯宁仇栾甘厉戎祖武符刘景詹束龙叶白司韶黎薄印宿怀蒲鄂索谭赖卓翟池乔阴胥能苍闻莘党翟谭申冉宰桂桑濮牛寿通边扈燕尚农温庄晏瞿茹习连艾向古廖耿满匡曾沙关查荆游竺权逯盖益桓公')

BUSINESS=['辅导','慢性病','赋能','服务','版权','登记','企业','公司','咨询','财税','税筹','园区','课程','顾问','营销','增长','运营','二手车','知识付费','投资','招商','推广','孵化','财商','支付','创业','工作室','学堂','收徒','开发','烘焙','蛋糕','豆浆','传媒','科技','文化','教育','培训','管理','直播','短视频','外卖','招生','房产','地产','装修','装饰','工程','保险','光伏','法务','资产','广告','供应链','网络','商贸','设计','电商','软件','美业','心理','瑜伽','调理','疗愈','号','集团','品牌','商学院','东证','证券','基金','银行','规划','升学','涨粉','城市','名字','职位','英语','语文','数学','牛肉','肉干','奶酪','推荐官','知识','人工在线','客服','私人定制','五常米','二手','通知','版','本人','助理','工作','商业','导师','美学','中心','机器人','家电','灯具','小红书','腾讯','字节','中医','朋友圈','小鹅通','家居','养生','防水','采购','留学','工厂','手作','商学','职教','康养','营养','超市','新希望','学长家','院校','招募','合作','代理','招聘','教培','商服','汽修','汽车']

PREFIXES=['人生设计师','独立设计师','文化活动推广','全媒体运营师','个人成长教练','摄影师','设计师','营养师','心理咨询师','咨询师','情感咨询师','健康管理师','健身教练','道医','猎头','律师','会计','策划师','程序员','资深','澳洲','加拿大','新西兰','新加坡','上海','北京','深圳','广州','杭州','成都','武汉','长沙','郑州','东莞','苏州','南京','西安','重庆','天津','海南','香港','台湾','美国','英国','日本','欧洲','主号','备用号','工作号','我是','我叫','这里是','叫我','我就是']

STOP_EN={'a','aa','aaa','ip','ai','opc','opt','ceo','coo','cfo','hr','vip','no','ms','mr','mrs','miss','dr','the','of','and','to','at','we','be','my','is','in','on','ltd','co','top','new','wechat','wx','id','china','group','media','studio','global','online','official','service','about','stop','go','life','love'}

PLACEHOLDERS={'微信用户','新手用户','客户','用户','未知用户','企微用户','未命名','游客','你好','待添加','暂无','不知道','随便','某某','联系人','老师','先生','女士','老板','校长','教练','院长','师傅','的手','总在','哥做','热爱版','成长版','工作号','本人','小灶班','创始人','朋友圈','手作小铺'}

SMALL=str.maketrans({'ᴇ':'e','ᴛ':'t','ᴀ':'a','ʀ':'r','ᴍ':'m','ɪ':'i','ʟ':'l','ᴏ':'o','ɴ':'n','ᴄ':'c','ʜ':'h','ᴋ':'k','ᴘ':'p','ᴡ':'w','ʏ':'y','ꜱ':'s'})

def normalize(raw):
 s=unicodedata.normalize('NFKC',raw).translate(SMALL)
 s=re.sub('[丨〡｜]','|',s)
 s=''.join(c for c in unicodedata.normalize('NFKD',s)if not unicodedata.combining(c))
 s=re.sub(r'(?:\+?86[- ]?)?1[3-9][0-9]{9}|\d{6,}',' ',s)
 s=''.join(c if c.isascii()or '\u4e00'<=c<='\u9fff' else ' 'for c in s)
 return re.sub(r'\s+',' ',s).strip()

def natural_cn(s):
 if not re.fullmatch(r'[\u4e00-\u9fff]{2,6}',s)or s in PLACEHOLDERS:return False
 if any(k in s for k in BUSINESS):return False
 return True

def decide(raw):
 if not raw.strip():return '宝子','owner_rule_missing_source'
 s=normalize(raw)
 if not s or s in PLACEHOLDERS or re.fullmatch(r'[\W\d_]+',s):return '你好','neutral_unusable_source'
 if re.search(r'(.)\1{3,}',s):return '你好','neutral_ambiguous_repetition'
 # Discard business/state parentheses; keep a named alias only when main text is descriptive.
 brackets=re.findall(r'[\(\[【]([^\)\]】]+)[\)\]】]',s)
 main=re.sub(r'[\(\[【][^\)\]】]*(?:[\)\]】]|$)',' ',s)
 main=re.sub(r'^A+(?=[\u4e00-\u9fff])','',main).strip()
 main=re.sub(r'^是(小[\u4e00-\u9fff]{1,2})呀$',r'\1',main)
 for p in PREFIXES:
  if main.startswith(p):main=main[len(p):].strip()
 # Whitespace between Chinese characters is decoration, not multiple names.
 parts=main.split()
 if len(parts)>1 and all(re.fullmatch(r'[\u4e00-\u9fff]{1,2}',x)for x in parts)and (all(len(x)==1 for x in parts)or len(''.join(parts))==3):main=''.join(parts)
 cn=re.findall(r'[\u4e00-\u9fff]+',main)
 if len(cn)==1 and len(cn[0])>=4 and not (cn[0][0]in SURNAMES and len(cn[0])<=4):
  bracket_name=next((b for b in brackets if natural_cn(b)and len(b)<=3 and not any(t in b for t in ['版','五行','本人','主号','微信'])),None)
  if bracket_name:return bracket_name,'chinese_parenthesized_name'
 # A delimiter-separated real name outranks an occupational slogan.
 # Reuse the same Chinese-name rule, without inventing a translation or alias.
 tail=re.search(r'(?:[-|])\s*([\u4e00-\u9fff]{2,3})\s*$',main)
 if tail and tail[1][0]in SURNAMES and natural_cn(tail[1]):
  name,_=decide(tail[1]);return name,'source_delimited_person_name'
 title=re.match(r'^([\u4e00-\u9fff](?:老师|先生|女士|教练|校长|院长))(.+)$',main)
 if title and title[1][0]in SURNAMES and any(t in title[2]for t in BUSINESS):
  return title[1],'source_honorific_before_business_remark'
 for prefix in sorted(BUSINESS,key=len,reverse=True):
  if main.startswith(prefix):
   rest=main[len(prefix):].strip()
   if natural_cn(rest)and rest.endswith(('哥','姐','老师','先生','女士','教练','校长','院长')):
    return rest,'source_nickname_after_business_prefix'
 en=re.findall(r'[A-Za-z]+',main)
 english=[t for t in en if 2<=len(t)<=15 and t.lower()not in STOP_EN and not re.fullmatch(r'(.)\1+',t,re.I)]
 # A recognisable source English name is kept; no invented translation.
 if english and english[0].lower()not in {'de','du','hello','all','sop','tpc','abcbook'}:
  strong_cn=next((c for c in cn if (len(c)==3 and c[0]in SURNAMES and natural_cn(c))or (c.startswith(('小','老','阿'))and 2<=len(c)<=4 and natural_cn(c))or (len(c)==2 and c[0]==c[1])),None)
  if not strong_cn:return english[0],'english_source_first_token'
 candidates=[]
 for i,c in enumerate(cn):
  c=c.strip('的')if len(c)>2 and c.endswith('的')else c
  for p in PREFIXES:
   if c.startswith(p):c=c[len(p):]
  # business remark followed by a source-visible name or honorific
  title=re.search(r'([\u4e00-\u9fff](?:老师|先生|女士|教练|校长|院长|师傅))$',c)
  if title and not (natural_cn(c)and len(c)<=6)and title.group(1)[0]in SURNAMES and not c.endswith(('商业教练','数学老师','营养教练')):
   c=title.group(1)
  if natural_cn(c):
   score=100-i*35
   if len(c)==3 and c[0]in SURNAMES and not c.startswith(('小','老','阿')):score=120-i*35
   if c.startswith(('小','老','阿'))and len(c)<=4:score=125-i*35
   if c.endswith(('老师','先生','女士','教练','校长','院长','叔','姐','哥')):score=100-i*35
   if len(c)>4:score=50-i*35
   candidates.append((score,c))
  else:
   # Separate a business prefix only when a delimiter/space or explicit role leaves a clear short token.
   m=re.search(r'(?:小|老|阿)[\u4e00-\u9fff]{1,2}$',c)
   if m and any(t in c for t in BUSINESS):candidates.append((72-i,m.group()))
 if candidates:
  _,name=max(candidates,key=lambda x:x[0])
  if len(name)==3 and name[0]in SURNAMES and not name.startswith(('小','老','阿'))and not name.endswith(('老师','先生','女士','教练','校长','院长','师傅','姐','哥','叔')):
   return name[-2:],'chinese_three_character_name_tail'
  if len(name)>3 and not name.endswith(('老师','先生','女士','教练','校长','院长')):
   m=re.search(r'的([\u4e00-\u9fff]{2,3})$',name)
   if m:return m.group(1),'chinese_named_object'
   if len(name)>4:return name[:2],'chinese_descriptive_prefix'
  return name,'chinese_name_token'
 if english:return english[0],'english_source_first_token'
 for b in brackets:
  if natural_cn(b)and len(b)<=3 and not any(t in b for t in ['版','五行','本人','主号','微信']):return b,'chinese_parenthesized_name'
 # A descriptive Chinese nickname may use its leading two characters, but business labels do not.
 if len(cn)==1 and len(cn[0])>4 and not any(t in cn[0]for t in BUSINESS):
  m=re.search(r'的([\u4e00-\u9fff]{2,3})$',cn[0])
  return (m.group(1)if m else cn[0][:2]),'chinese_descriptive_prefix'
 return '你好','neutral_no_reliable_name'
