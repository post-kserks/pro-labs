import { FormEvent, useState } from "react";

export function LoginScreen({
  onLogin,
  hadToken,
}: {
  onLogin: (token: string) => void;
  hadToken: boolean;
}) {
  const [value, setValue] = useState("");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (value.trim()) onLogin(value.trim());
  };

  return (
    <div className="login-screen">
      <form className="login-card" onSubmit={submit}>
        <div className="logo logo-big">
          Vault<span className="logo-accent">DB</span>
        </div>
        <p className="login-hint">
          {hadToken
            ? "The saved token was rejected. Enter a valid API token."
            : "Enter an API token to connect (see VAULTDB_API_TOKENS or server logs)."}
        </p>
        <input
          type="password"
          placeholder="vdb_sk_…"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          autoFocus
          aria-label="API token"
        />
        <button type="submit" className="btn btn-primary" disabled={!value.trim()}>
          Connect
        </button>
      </form>
    </div>
  );
}
