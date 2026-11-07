// Reuse the official provider's verified stream. No model-selected host paths.
export async function attachmentText(attachments, ref, signal) {
  if(ref.bytes>256*1024)throw Error('附件超过256KiB文本导入限额，请通过标准文档连接器处理原文件');
  const parts=[];let size=0;
  for await(const chunk of attachments.readFileStream(ref,signal)) {
    size+=chunk.length;if(size>256*1024)throw Error('Attachment exceeds text limit');parts.push(chunk);
  }
  const bytes=Buffer.concat(parts);
  const text=new TextDecoder('utf-8',{fatal:true}).decode(bytes);
  if(text.includes('\u0000'))throw Error('此附件不是UTF-8文本，请配置对应格式的标准文档连接器');
  return text;
}
