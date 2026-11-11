import {basicSetup, EditorView} from 'codemirror';
import {markdown} from '@codemirror/lang-markdown';

export function openEditor(parent, text, update) {
  const view = new EditorView({
    parent,
    doc: text,
    extensions: [
      basicSetup,
      markdown(),
      EditorView.lineWrapping,
      EditorView.contentAttributes.of({'aria-label':'笔记正文'}),
      EditorView.updateListener.of(change => {
        if (change.docChanged) update(change.state.doc.toString());
      }),
    ],
  });
  view.focus();
  return () => view.destroy();
}
