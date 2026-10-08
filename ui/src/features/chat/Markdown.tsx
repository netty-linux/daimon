import { Fragment, useState, type ReactNode } from 'react';
import { pt } from '../../i18n/pt-BR';

// A deliberately small Markdown renderer. React escapes every text node; no HTML
// parser, innerHTML, image embedding or protocol-relative/executable URLs.
function inline(text: string): ReactNode[] {
  return text.split(/(`[^`\n]+`|\*\*[^*\n]+\*\*|\[[^\]\n]+\]\([^\s)]+\))/g).map((part, index) => {
    if (part.startsWith('`') && part.endsWith('`')) return <code key={index}>{part.slice(1, -1)}</code>;
    if (part.startsWith('**') && part.endsWith('**')) return <strong key={index}>{part.slice(2, -2)}</strong>;
    const link = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(part);
    if (link && /^https?:\/\/[^\s]+$/i.test(link[2])) return <a key={index} href={link[2]} target="_blank" rel="noopener noreferrer">{link[1]}</a>;
    return part;
  });
}
export function CopyText({ content }: { content: string }) {
  const [copied, setCopied] = useState(false), [error, setError] = useState(false);
  return <span className="copy-action"><button type="button" onClick={() => {
    void (async () => { try { if (!navigator.clipboard) throw new Error(); await navigator.clipboard.writeText(content); setCopied(true); setError(false); } catch { setError(true); } })();
  }}>{copied ? pt.copied : pt.copy}</button>{error && <span role="status">{pt.copyFailed}</span>}</span>;
}
function CodeBlock({ language, content }: { language: string; content: string }) {
  return <div className="code-block"><header><span>{language || 'Código'}</span><CopyText content={content}/></header><pre><code>{content}</code></pre></div>;
}
export function Markdown({ content }: { content: string }) {
  const parts = content.split(/(^```[^\n]*\n[\s\S]*?^```\s*$)/gm);
  return <div className="markdown">{parts.map((part, partIndex) => {
    const fenced = /^```([^\n]*)\n([\s\S]*?)\n?```\s*$/.exec(part);
    if (fenced) return <CodeBlock key={partIndex} language={fenced[1].trim()} content={fenced[2]} />;
    const lines = part.split('\n'); const blocks: ReactNode[] = [];
    for (let index = 0; index < lines.length; index++) {
      const line = lines[index];
      if (line.startsWith('|') && /^\|?[\s:|-]+\|?$/.test(lines[index + 1] ?? '') && (lines[index + 1] ?? '').includes('-')) {
        const cells = (value: string) => value.replace(/^\||\|$/g, '').split('|').map(cell => cell.trim());
        const headings = cells(line), rows: string[][] = []; index += 2;
        while (index < lines.length && lines[index].startsWith('|')) rows.push(cells(lines[index++])); index--;
        blocks.push(<div key={index} className="table-scroll"><table><thead><tr>{headings.map((cell, i) => <th key={i}>{inline(cell)}</th>)}</tr></thead><tbody>{rows.map((row, i) => <tr key={i}>{row.map((cell, j) => <td key={j}>{inline(cell)}</td>)}</tr>)}</tbody></table></div>);
      } else if (/^#{1,6} /.test(line)) {
        const text = line.replace(/^#{1,6} /, ''); blocks.push(<h3 key={index}>{inline(text)}</h3>);
      } else if (/^\s*([-*]|\d+\.) /.test(line)) {
        const ordered = /^\s*\d+\./.test(line), entries: string[] = [];
        while (index < lines.length && /^\s*([-*]|\d+\.) /.test(lines[index])) entries.push(lines[index++].replace(/^\s*([-*]|\d+\.) /, '')); index--;
        const items = entries.map((entry, i) => <li key={i}>{inline(entry)}</li>);
        blocks.push(ordered ? <ol key={index}>{items}</ol> : <ul key={index}>{items}</ul>);
      } else if (line.startsWith('> ')) blocks.push(<blockquote key={index}>{inline(line.slice(2))}</blockquote>);
      else blocks.push(<Fragment key={index}>{inline(line)}{index < lines.length - 1 && '\n'}</Fragment>);
    }
    return <div className="markdown-text" key={partIndex}>{blocks}</div>;
  })}</div>;
}
