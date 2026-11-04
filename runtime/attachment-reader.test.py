import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
import zipfile
spec=importlib.util.spec_from_file_location('reader',Path(__file__).with_name('attachment-reader.py'))
reader=importlib.util.module_from_spec(spec);spec.loader.exec_module(reader)

class AttachmentReader(unittest.TestCase):
 def extract(self,name,data):
  with tempfile.TemporaryDirectory() as folder:
   p=Path(folder)/'input';p.write_bytes(data);return reader.extract(str(p),name)
 def zipped(self,parts):
  out=io.BytesIO()
  with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
   for name,data in parts.items():z.writestr(name,data)
  return out.getvalue()
 def test_unicode_text_and_limits(self):
  self.assertEqual(self.extract('记录.md','真实附件校验7319'.encode())['text'],'真实附件校验7319')
  with self.assertRaises(reader.ReadError):self.extract('x.txt',b'a'*(reader.MAX_TEXT+1))
  with self.assertRaises(reader.ReadError):self.extract('x.bin',b'\0private')
 def test_document_and_presentation(self):
  data=self.zipped({'word/document.xml':'<w:document xmlns:w="w"><w:p><w:r><w:t>合同金额</w:t></w:r><w:r><w:t>7319</w:t></w:r></w:p><w:p><w:r><w:t>第二行</w:t></w:r></w:p></w:document>'})
  self.assertEqual(self.extract('合同.docx',data)['text'],'合同金额7319\n第二行')
  data=self.zipped({'ppt/slides/slide1.xml':'<a:sp xmlns:a="a"><a:p><a:r><a:t>幻灯片7319</a:t></a:r></a:p></a:sp>'})
  self.assertIn('第1页\n幻灯片7319',self.extract('说明.pptx',data)['text'])
 def test_spreadsheet_resolves_shared_values_and_cached_formula(self):
  data=self.zipped({'xl/sharedStrings.xml':'<sst><si><t>校验名称</t></si></sst>','xl/_rels/workbook.xml.rels':'<Relationships><Relationship Id="r1" Target="worksheets/sheet1.xml"/></Relationships>','xl/workbook.xml':'<workbook xmlns:r="r"><sheets><sheet name="验收" r:id="r1"/></sheets></workbook>','xl/worksheets/sheet1.xml':'<worksheet><sheetData><row><c r="A1" t="s"><v>0</v></c><c r="B1"><v>7319</v></c><c r="C1"><f>B1+1</f><v>7320</v></c></row></sheetData></worksheet>'})
  v=self.extract('表格.xlsx',data)['text'];self.assertIn('A1: 校验名称\tB1: 7319',v);self.assertIn('未重算',v)
 def test_entity_and_compression_bombs_are_not_parsed(self):
  for xml in ['<!DOCTYPE p [<!ENTITY x SYSTEM "file:///etc/passwd">]><document>&x;</document>','x'*(reader.MAX_XML+1)]:
   with self.assertRaises(reader.ReadError):self.extract('bad.docx',self.zipped({'word/document.xml':xml}))
 def test_subprocess_output_and_time_limits(self):
  import sys
  with self.assertRaises(reader.ReadError):reader.run([sys.executable,'-c',"import sys;sys.stdout.write('x'*10000000)"])
  with self.assertRaises(reader.ReadError):reader.run([sys.executable,'-c','import time;time.sleep(10)'],timeout=.1)
 def test_real_pdf_and_image_text(self):
  root=os.environ.get('PW_ATTACHMENT_FIXTURES')
  if not root:self.skipTest('set real PDF/image fixture directory')
  for name,kind in [('proof.pdf','pdf'),('proof.png','ocr')]:
   result=reader.extract(str(Path(root)/name),name)
   self.assertEqual(result['kind'],kind);self.assertIn('7319',result['text'])
   if kind=='ocr':self.assertIn('未分析',result['warning'])

if __name__=='__main__':unittest.main()
