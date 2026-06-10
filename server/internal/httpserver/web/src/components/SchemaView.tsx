import { useEffect, useState } from "react";
import { getTableSchema, TableSchemaInfo } from "../api/vaultdb";

export function SchemaView({
  token,
  db,
  table,
  onClose,
}: {
  token: string;
  db: string;
  table: string;
  onClose: () => void;
}) {
  const [schema, setSchema] = useState<TableSchemaInfo | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setSchema(null);
    setError(null);
    getTableSchema(token, db, table)
      .then(setSchema)
      .catch((e: Error) => setError(e.message));
  }, [token, db, table]);

  return (
    <div className="schema-view">
      <div className="schema-header">
        <span>
          {db}.{table}
        </span>
        <button className="btn btn-ghost btn-small" onClick={onClose}>
          ✕
        </button>
      </div>
      {error && <p className="result-error">{error}</p>}
      {schema && (
        <table className="result-table schema-table">
          <thead>
            <tr>
              <th>column</th>
              <th>type</th>
            </tr>
          </thead>
          <tbody>
            {schema.columns.map((col) => (
              <tr key={col.name}>
                <td>{col.name}</td>
                <td>
                  {col.type}
                  {col.varchar_len ? `(${col.varchar_len})` : ""}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {schema && <div className="result-meta">{schema.row_count} rows</div>}
    </div>
  );
}
