import { QueryResult } from "../api/vaultdb";
import { ExplainView } from "./ExplainView";

export function ResultTable({ result }: { result: QueryResult | null }) {
  if (!result) {
    return <div className="placeholder">Run a query to see results.</div>;
  }

  if (result.status === "error") {
    return (
      <div className="result-error">
        <p>
          {result.error_code ? `Error ${result.error_code}: ` : "Error: "}
          {result.message}
        </p>
      </div>
    );
  }

  if (result.type === "affected") {
    return (
      <div className="result-info result-ok">
        ✓ Affected rows: {result.affected} ({result.duration_ms.toFixed(1)} ms)
      </div>
    );
  }

  if (result.type === "message") {
    return <ExplainView message={result.message || ""} durationMs={result.duration_ms} />;
  }

  return (
    <div className="result-table-wrap">
      <div className="result-meta">
        {result.rows.length} rows · {result.duration_ms.toFixed(1)} ms
        {result.as_of_note ? ` · ${result.as_of_note}` : ""}
      </div>
      <table className="result-table">
        <thead>
          <tr>
            {result.columns.map((col) => (
              <th key={col}>{col}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {result.rows.map((row, i) => (
            <tr key={i}>
              {row.map((cell, j) => (
                <td key={j} className={cell === null ? "cell-null" : ""}>
                  {cell === null ? "NULL" : String(cell)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
