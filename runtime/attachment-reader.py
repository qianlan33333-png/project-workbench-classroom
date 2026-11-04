#!/usr/bin/env python3
"""Fixed, offline attachment text reader. Never executes document content."""
import io
import json
import os
from pathlib import Path
import re
import selectors
import subprocess
import sys
import time
import xml.etree.ElementTree as ET
import zipfile

MAX_INPUT = 32 * 1024**2
MAX_TEXT = 2 * 1024**2
MAX_XML = 8 * 1024**2
MAX_EXPANDED = 32 * 1024**2
MAX_PAGES = 50

class ReadError(Exception):
    pass

def bounded_text(text):
    if len(text.encode('utf-8')) > MAX_TEXT:
        raise ReadError('可读取内容超过2MiB，请拆分文件后重试')
    return text

def run(args, timeout=22):
    # Drain into a bounded buffer; neither stderr nor stdout can fill disk.
    try:p = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    except FileNotFoundError:raise ReadError('当前环境未安装该格式的读取组件')
    deadline, data = time.monotonic()+timeout, bytearray()
    try:
        with selectors.DefaultSelector() as poll:
            poll.register(p.stdout, selectors.EVENT_READ)
            while poll.get_map():
                remaining=deadline-time.monotonic()
                if remaining<=0:raise ReadError('文件读取超时，请缩小文件后重试')
                for key,_ in poll.select(min(remaining,.2)):
                    chunk=os.read(key.fileobj.fileno(),65536)
                    if not chunk:poll.unregister(key.fileobj);continue
                    data.extend(chunk)
                    if len(data)>MAX_TEXT*2:raise ReadError('提取内容过大，请拆分文件后重试')
            try:code=p.wait(timeout=max(.001,deadline-time.monotonic()))
            except subprocess.TimeoutExpired:raise ReadError('文件读取超时，请缩小文件后重试')
        if code:raise ReadError('文件无法读取，可能已加密或格式损坏')
        return data.decode('utf-8', errors='replace')
    finally:
        if p.poll() is None:p.kill()
        p.wait();p.stdout.close()

def openxml(data, suffix):
    # Reject oversized/ZIP64 directories before ZipFile builds an in-memory index.
    at = data.rfind(b'PK\x05\x06', max(0, len(data)-65557))
    if at < 0 or at+22 > len(data) or int.from_bytes(data[at+10:at+12], 'little') > 5000:
        raise ReadError('文档压缩结构无效或内容过多')
    z = zipfile.ZipFile(io.BytesIO(data))
    infos = z.infolist()
    if len(infos) > 5000 or len({x.filename for x in infos}) != len(infos) or sum(x.file_size for x in infos) > MAX_EXPANDED:
        raise ReadError('文档展开后过大或结构异常，请拆分后重试')
    total = 0
    def xml(name):
        nonlocal total
        info = z.getinfo(name)
        if info.file_size > MAX_XML or info.flag_bits & 1:
            raise ReadError('文档已加密或单个内容块过大')
        with z.open(info) as f:
            raw = f.read(MAX_XML+1)
        total += len(raw)
        if len(raw) > MAX_XML or total > MAX_EXPANDED or re.search(br'<!\s*(?:DOCTYPE|ENTITY)', raw, re.I):
            raise ReadError('文档结构不受支持或内容过大')
        return ET.fromstring(raw)
    local = lambda n: n.tag.rsplit('}',1)[-1]
    text = lambda n: ''.join(x.text or '' for x in n.iter() if local(x) == 't')
    if suffix == '.docx':
        root = xml('word/document.xml')
        result = '\n'.join(text(n) for n in root.iter() if local(n)=='p')
    elif suffix == '.pptx':
        slides = sorted((x.filename for x in infos if re.fullmatch(r'ppt/slides/slide\d+\.xml', x.filename)), key=lambda n:int(re.search(r'(\d+)\.xml$',n).group(1)))
        if len(slides)>MAX_PAGES:
            raise ReadError('演示文稿超过50页，请拆分后重试')
        result = '\n\n'.join(f'第{i+1}页\n'+'\n'.join(text(n) for n in xml(name).iter() if local(n)=='p') for i,name in enumerate(slides))
    else:
        shared = [text(n) for n in xml('xl/sharedStrings.xml') if local(n)=='si'] if 'xl/sharedStrings.xml' in z.namelist() else []
        links = {n.attrib['Id']: n.attrib['Target'] for n in xml('xl/_rels/workbook.xml.rels') if n.attrib.get('TargetMode')!='External'}
        output, count = [], 0
        for sheet in xml('xl/workbook.xml').iter():
            if local(sheet)!='sheet':continue
            rid = next((v for k,v in sheet.attrib.items() if k.endswith('}id')), '')
            target = links.get(rid,'')
            name = target.lstrip('/') if target.startswith('/') else 'xl/'+target
            if not re.fullmatch(r'xl/worksheets/[^/]+\.xml', name):
                raise ReadError('工作表引用不受支持')
            output.append('工作表：'+sheet.attrib.get('name',''))
            for row in xml(name).iter():
                if local(row)!='row':continue
                cells=[]
                for cell in row:
                    if local(cell)!='c':continue
                    count+=1
                    if count>50000:raise ReadError('工作表超过50000个单元格，请拆分后重试')
                    value = next((x.text or '' for x in cell if local(x)=='v'),'')
                    formula = next((x.text or '' for x in cell if local(x)=='f'),'')
                    if cell.attrib.get('t')=='s':
                        try:
                            index=int(value)
                            if index<0:raise IndexError()
                            value=shared[index]
                        except (ValueError,IndexError):raise ReadError('工作表文本索引损坏')
                    elif cell.attrib.get('t')=='inlineStr':value=text(cell)
                    if formula:value+=f' [公式={formula}，仅使用文件缓存值，未重算]'
                    cells.append(cell.attrib.get('r','')+': '+value)
                output.append('\t'.join(cells))
        result='\n'.join(output)
    return {'text': bounded_text(result), 'kind':'document'}

def extract(path, filename):
    if os.path.getsize(path)>MAX_INPUT:
        raise ReadError('读取文件最多32MiB，请缩小或拆分后重试')
    suffix=Path(filename).suffix.lower()
    if suffix in ('.pdf','.png','.jpg','.jpeg','.webp','.bmp','.tif','.tiff','.gif'):
        if sys.platform=='darwin':
            helper=Path(__file__).parent/'.local'/'attachment-reader-macos'
            if not helper.is_file():raise ReadError('本机开发读取组件未构建，请先运行 scripts/build-macos-attachment-reader.sh')
            result=json.loads(run([str(helper),path,'pdf' if suffix=='.pdf' else 'ocr']))
        elif suffix=='.pdf':
            info=run(['pdfinfo',path])
            match=re.search(r'^Pages:\s*(\d+)',info,re.M)
            if not match or int(match.group(1))>MAX_PAGES:raise ReadError('PDF最多读取50页，请拆分后重试')
            pages=run(['pdftotext','-layout','-enc','UTF-8',path,'-']).split('\f')[:int(match.group(1))]
            blank=[i+1 for i,p in enumerate(pages) if not p.strip()]
            result={'text':'\n\n'.join(f'第{i+1}页\n'+p.strip() for i,p in enumerate(pages) if p.strip()),'kind':'pdf'}
            if blank:result['warning']='部分页面没有可提取文字，可能为扫描页；未读取页：'+','.join(map(str,blank))
        else:
            result={'text':run(['tesseract',path,'stdout','-l','chi_sim+eng','--psm','3']),'kind':'ocr'}
        if not result.get('text','').strip():raise ReadError('没有可提取的文字，扫描PDF可先转为图片；图片仅支持文字识别')
        if result.get('kind')=='ocr':result['warning']='这是图片文字识别结果，可能存在误识别；未分析非文字图像内容。'
        result['text']=bounded_text(result['text']);return result
    with open(path,'rb') as f:data=f.read(MAX_INPUT+1)
    if suffix in ('.docx','.xlsx','.pptx'):return openxml(data,suffix)
    if b'\0' in data:raise ReadError('当前格式无法读取，请转换为文本、PDF或Office文档')
    try:text=data.decode('utf-8-sig')
    except UnicodeDecodeError:raise ReadError('当前编码无法读取，请另存为UTF-8文本或PDF')
    return {'text':bounded_text(text),'kind':'text'}

if __name__=='__main__':
    try:
        if len(sys.argv)!=3:raise ReadError('读取参数无效')
        print(json.dumps(extract(sys.argv[1],sys.argv[2]),ensure_ascii=False))
    except Exception as e:
        print(json.dumps({'error':str(e) if isinstance(e,ReadError) else '文件格式损坏或不受支持'},ensure_ascii=False));sys.exit(1)
