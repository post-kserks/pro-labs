import { useSyncExternalStore } from 'react';
import { TOKEN_KEY } from '../api/client';
import type { User } from '../api/types';

const USER_KEY = 'medvault_user';

interface AuthState {
  token: string | null;
  user: User | null;
}

function read(): AuthState {
  const token = localStorage.getItem(TOKEN_KEY);
  const rawUser = localStorage.getItem(USER_KEY);
  let user: User | null = null;
  if (rawUser) {
    try {
      user = JSON.parse(rawUser) as User;
    } catch {
      user = null;
    }
  }
  return { token, user };
}

let state: AuthState = read();
const listeners = new Set<() => void>();

function emit() {
  state = read();
  listeners.forEach((l) => l());
}

export function setAuth(token: string, user: User) {
  localStorage.setItem(TOKEN_KEY, token);
  localStorage.setItem(USER_KEY, JSON.stringify(user));
  emit();
}

export function clearAuth() {
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
  emit();
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

export function useAuth(): AuthState & { logout: () => void } {
  const snapshot = useSyncExternalStore(subscribe, () => state);
  return { ...snapshot, logout: clearAuth };
}
