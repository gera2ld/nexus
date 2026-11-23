// Monaco, in its own bundle: this module is only loaded when a text file
// is opened. Only the editor core and syntax highlighting are included;
// the language services (TypeScript, JSON, CSS, HTML) and their large
// workers are left out.
import * as monaco from "monaco-editor/editor/editor.api";
import "monaco-editor/basic-languages/monaco.contribution";
import EditorWorker from "monaco-editor/editor/editor.worker?worker";
import { useEffect, useRef } from "react";

self.MonacoEnvironment = { getWorker: () => new EditorWorker() };

/** Monaco's language for a file name, from its extension or exact name. */
function languageFor(name: string) {
  const lower = name.toLowerCase();
  const ext = lower.includes(".") ? lower.slice(lower.lastIndexOf(".")) : "";
  const langs = monaco.languages.getLanguages();
  return (
    langs.find((l) => l.filenames?.some((f) => f.toLowerCase() === lower))?.id ??
    (ext && langs.find((l) => l.extensions?.includes(ext))?.id) ??
    "plaintext"
  );
}

const dark = matchMedia("(prefers-color-scheme: dark)");
const theme = () => (dark.matches ? "vs-dark" : "vs");

export default function Editor(props: {
  name: string;
  value: string;
  readOnly: boolean;
  onChange: (value: string) => void;
  onSave: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const ed = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  // The latest callbacks, so the editor needn't be rebuilt when they change.
  const cb = useRef(props);
  cb.current = props;

  useEffect(() => {
    const e = monaco.editor.create(box.current!, {
      value: props.value,
      language: languageFor(props.name),
      readOnly: props.readOnly,
      theme: theme(),
      automaticLayout: true,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      fontSize: 13,
      tabSize: 2,
      unicodeHighlight: { ambiguousCharacters: false },
    });
    ed.current = e;
    const sub = e.onDidChangeModelContent(() => cb.current.onChange(e.getValue()));
    e.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => cb.current.onSave());
    const onTheme = () => monaco.editor.setTheme(theme());
    dark.addEventListener("change", onTheme);
    return () => {
      dark.removeEventListener("change", onTheme);
      sub.dispose();
      e.getModel()?.dispose();
      e.dispose();
    };
    // value is only the starting text: FileView gives a different file, or
    // a reload from the server, a new key and so a new editor.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => ed.current?.updateOptions({ readOnly: props.readOnly }), [props.readOnly]);

  return <div ref={box} className="h-full min-h-0 w-full" />;
}
