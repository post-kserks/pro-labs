import { differenceInYears, format, parseISO } from 'date-fns';
import { ru } from 'date-fns/locale';

export function safeParse(iso?: string | null): Date | null {
  if (!iso) return null;
  try {
    const d = parseISO(iso);
    if (isNaN(d.getTime())) return null;
    return d;
  } catch {
    return null;
  }
}

export function fmtDate(iso?: string | null, pattern = 'd MMM yyyy'): string {
  const d = safeParse(iso);
  return d ? format(d, pattern, { locale: ru }) : '—';
}

export function fmtDateTime(iso?: string | null): string {
  return fmtDate(iso, 'd MMM yyyy, HH:mm');
}

export function age(birthDate?: string | null): number | null {
  const d = safeParse(birthDate);
  return d ? differenceInYears(new Date(), d) : null;
}

export function severityLabel(s: string): string {
  switch (s) {
    case 'mild':
      return 'Лёгкая';
    case 'moderate':
      return 'Умеренная';
    case 'severe':
      return 'Тяжёлая';
    case 'life_threatening':
      return 'Угрожающая жизни';
    default:
      return s;
  }
}

export function statusLabel(s: string): string {
  switch (s) {
    case 'scheduled':
      return 'Ожидает';
    case 'in_progress':
      return 'В процессе';
    case 'completed':
      return 'Завершён';
    case 'cancelled':
      return 'Отменён';
    default:
      return s;
  }
}
