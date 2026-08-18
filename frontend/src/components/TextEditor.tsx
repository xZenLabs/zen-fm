import { useEffect, useMemo, useRef } from 'react'
import CodeMirror, { Decoration, EditorView, ViewPlugin, type DecorationSet } from '@uiw/react-codemirror'
import { css } from '@codemirror/lang-css'
import { html } from '@codemirror/lang-html'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { markdown } from '@codemirror/lang-markdown'
import { StreamLanguage } from '@codemirror/language'
import { Prec, RangeSetBuilder } from '@codemirror/state'
import { go } from '@codemirror/legacy-modes/mode/go'
import { lua } from '@codemirror/legacy-modes/mode/lua'
import { properties } from '@codemirror/legacy-modes/mode/properties'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import { toml } from '@codemirror/legacy-modes/mode/toml'
import { xml } from '@codemirror/legacy-modes/mode/xml'
import { yaml } from '@codemirror/legacy-modes/mode/yaml'
import { useTheme } from '@mui/material'
import { cspNonce } from '../emotion'

const legacyLanguages = {
  go: StreamLanguage.define(go),
  ini: StreamLanguage.define(properties),
  lua: StreamLanguage.define(lua),
  sh: StreamLanguage.define(shell),
  toml: StreamLanguage.define(toml),
  xml: StreamLanguage.define(xml),
  yaml: StreamLanguage.define(yaml),
}

const editorSecurity = cspNonce ? [EditorView.cspNonce.of(cspNonce)] : []

type FindHighlight = { query: string; current?: { from: number; to: number } }

function findHighlightExtension(find: FindHighlight) {
  const needle = find.query.toLocaleLowerCase()
  const matchMark = Decoration.mark({ class: 'cm-zen-find-match' })
  const currentMark = Decoration.mark({ class: 'cm-zen-find-match cm-zen-find-current' })
  return ViewPlugin.fromClass(class {
    decorations: DecorationSet

    constructor(view: EditorView) {
      this.decorations = this.build(view)
    }

    update(update: { view: EditorView; docChanged: boolean; viewportChanged: boolean }) {
      if (update.docChanged || update.viewportChanged) this.decorations = this.build(update.view)
    }

    build(view: EditorView) {
      if (!needle) return Decoration.none
      const builder = new RangeSetBuilder<Decoration>()
      let lastAdded = -1
      for (const visible of view.visibleRanges) {
        const searchFrom = Math.max(0, visible.from - needle.length + 1)
        const searchTo = Math.min(view.state.doc.length, visible.to + needle.length - 1)
        const visibleText = view.state.doc.sliceString(searchFrom, searchTo).toLocaleLowerCase()
        for (let match = visibleText.indexOf(needle); match !== -1; match = visibleText.indexOf(needle, match + needle.length)) {
          const from = searchFrom + match
          const to = from + needle.length
          if (from <= lastAdded || to <= visible.from || from >= visible.to) continue
          const current = find.current?.from === from && find.current.to === to
          builder.add(from, to, current ? currentMark : matchMark)
          lastAdded = from
        }
      }
      return builder.finish()
    }
  }, { decorations: (plugin) => plugin.decorations })
}

function languageForName(name: string) {
  const filename = name.toLowerCase()
  const extension = filename.split('.').pop() ?? ''
  if (extension === 'json') return json()
  if (['md', 'markdown'].includes(extension)) return markdown()
  if (extension === 'js') return javascript()
  if (extension === 'jsx') return javascript({ jsx: true })
  if (extension === 'ts') return javascript({ typescript: true })
  if (extension === 'tsx') return javascript({ jsx: true, typescript: true })
  if (['html', 'htm', 'xhtml'].includes(extension)) return html()
  if (extension === 'css') return css()
  if (['yaml', 'yml'].includes(extension)) return legacyLanguages.yaml
  if (['sh', 'bash', 'zsh'].includes(extension) || ['.bashrc', '.zshrc', '.profile'].includes(filename)) return legacyLanguages.sh
  if (extension === 'lua') return legacyLanguages.lua
  if (extension === 'go') return legacyLanguages.go
  if (extension === 'toml') return legacyLanguages.toml
  if (extension === 'ini') return legacyLanguages.ini
  if (extension === 'xml') return legacyLanguages.xml
  return null
}

export default function TextEditor({ name, value, onChange, readOnly = false, fullHeight = false, find }: { name: string; value: string; onChange?: (value: string) => void; readOnly?: boolean; fullHeight?: boolean; find?: FindHighlight }) {
  const theme = useTheme()
  const viewRef = useRef<EditorView | null>(null)
  const findQuery = find?.query ?? ''
  const currentFindFrom = find?.current?.from
  const currentFindTo = find?.current?.to
  const hasFind = find !== undefined
  const language = useMemo(() => languageForName(name), [name])
  const surface = theme.palette.background.paper
  const surfaceTheme = useMemo(() => Prec.highest(EditorView.theme({
    '&.cm-editor': { backgroundColor: surface },
    '&.cm-editor .cm-gutters': { backgroundColor: surface },
  })), [surface])
  const findExtension = useMemo(() => !hasFind ? null : findHighlightExtension({
    query: findQuery,
    current: currentFindFrom === undefined || currentFindTo === undefined ? undefined : { from: currentFindFrom, to: currentFindTo },
  }), [currentFindFrom, currentFindTo, findQuery, hasFind])
  const extensions = useMemo(() => {
    const configured = language ? [...editorSecurity, language, surfaceTheme] : [...editorSecurity, surfaceTheme]
    return findExtension ? [...configured, findExtension] : configured
  }, [findExtension, language, surfaceTheme])
  useEffect(() => {
    if (currentFindFrom === undefined || !viewRef.current) return
    viewRef.current.dispatch({ effects: EditorView.scrollIntoView(currentFindFrom, { y: 'center' }) })
  }, [currentFindFrom])
  const fillsContainer = fullHeight || !readOnly
  return (
    <CodeMirror
      value={value}
      height={fillsContainer ? '100%' : undefined}
      minHeight={!fillsContainer ? '240px' : undefined}
      maxHeight={!fillsContainer ? '70vh' : undefined}
      theme={theme.palette.mode}
      extensions={extensions}
      editable={!readOnly}
      readOnly={readOnly}
      onChange={onChange}
      onCreateEditor={(view) => { viewRef.current = view }}
      basicSetup={{
        lineNumbers: true,
        foldGutter: false,
        highlightActiveLine: !readOnly,
        highlightActiveLineGutter: !readOnly,
        searchKeymap: !hasFind,
      }}
      style={{ height: fillsContainer ? '100%' : undefined }}
    />
  )
}
