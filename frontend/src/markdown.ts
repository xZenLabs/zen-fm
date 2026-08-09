import DOMPurify from 'dompurify'
import { marked } from 'marked'

const markdownTags = [
  'p', 'br', 'hr', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'blockquote', 'pre', 'code',
  'strong', 'em', 'del', 'ul', 'ol', 'li', 'a', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
]

function escapeRawHTML(source: string) {
  return source.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
}

export function renderMarkdown(source: string) {
  const parsed = marked.parse(escapeRawHTML(source), { async: false, gfm: true, breaks: false })
  return DOMPurify.sanitize(parsed, {
    ALLOWED_TAGS: markdownTags,
    ALLOWED_ATTR: ['href', 'title'],
    ALLOW_DATA_ATTR: false,
  })
}
