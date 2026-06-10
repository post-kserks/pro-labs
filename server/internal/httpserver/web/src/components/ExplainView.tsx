// ExplainView отображает текстовые ответы сервера (EXPLAIN-планы, сообщения
// DDL и т.п.) моноширинным блоком с сохранением форматирования.
export function ExplainView({ message, durationMs }: { message: string; durationMs: number }) {
  return (
    <div className="explain-view">
      <div className="result-meta">{durationMs.toFixed(1)} ms</div>
      <pre>{message}</pre>
    </div>
  );
}
