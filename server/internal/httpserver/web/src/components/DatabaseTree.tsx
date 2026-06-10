import { useState } from "react";
import { TableInfo } from "../api/vaultdb";

export function DatabaseTree({
  databases,
  tables,
  currentDB,
  onSelectDB,
  onExpandDB,
  onPickTable,
  onRefresh,
}: {
  databases: string[];
  tables: Record<string, TableInfo[]>;
  currentDB: string;
  onSelectDB: (db: string) => void;
  onExpandDB: (db: string) => Promise<void>;
  onPickTable: (db: string, table: string) => void;
  onRefresh: () => void;
}) {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const toggle = (db: string) => {
    const open = !expanded[db];
    setExpanded((prev) => ({ ...prev, [db]: open }));
    onSelectDB(db);
    if (open && !tables[db]) {
      void onExpandDB(db);
    }
  };

  return (
    <nav className="db-tree">
      <div className="db-tree-header">
        <span>Databases</span>
        <button className="btn btn-ghost btn-small" onClick={onRefresh} title="Refresh">
          ⟳
        </button>
      </div>
      {databases.length === 0 && <p className="db-tree-empty">No databases yet.</p>}
      <ul>
        {databases.map((db) => (
          <li key={db}>
            <button
              className={`tree-item ${db === currentDB ? "tree-item-active" : ""}`}
              onClick={() => toggle(db)}
            >
              {expanded[db] ? "▾" : "▸"} {db}
            </button>
            {expanded[db] && (
              <ul className="tree-tables">
                {(tables[db] || []).map((t) => (
                  <li key={t.name}>
                    <button className="tree-item tree-table" onClick={() => onPickTable(db, t.name)}>
                      ▦ {t.name}
                      <span className="row-count">{t.row_count}</span>
                    </button>
                  </li>
                ))}
                {tables[db] && tables[db].length === 0 && (
                  <li className="db-tree-empty">no tables</li>
                )}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </nav>
  );
}
