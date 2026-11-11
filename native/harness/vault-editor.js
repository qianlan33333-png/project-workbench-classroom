import {basicSetup, EditorView} from 'codemirror';
import {markdown} from '@codemirror/lang-markdown';
import {Compartment, EditorState} from '@codemirror/state';

export function openEditor(parent, text, update, readOnly) {
  const editing = new Compartment();
  const mode = locked => [EditorState.readOnly.of(locked), EditorView.editable.of(!locked)];
  const crlf = text.includes('\r\n') && !/(?:^|[^\r])\n|\r(?!\n)/.test(text);
  const view = new EditorView({
    parent,
    doc: text,
    extensions: [
      basicSetup,
      markdown(),
      editing.of(mode(readOnly)),
      ...(crlf ? [EditorState.lineSeparator.of('\r\n')] : []),
      EditorView.lineWrapping,
      EditorView.contentAttributes.of({'aria-label':'笔记正文'}),
      EditorView.updateListener.of(change => {
        if (change.docChanged) update(change.state.sliceDoc());
      }),
    ],
  });
  if (!readOnly) view.focus();
  return {
    destroy: () => view.destroy(),
    setReadOnly: locked => view.dispatch({effects: editing.reconfigure(mode(locked))}),
  };
}
