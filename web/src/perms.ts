import React from "react";

// Разрешения текущего пользователя (ТЗ: UI скрывает недоступные действия;
// авторизация всё равно на backend). "*" — все права (dev-режим).
export const PermsContext = React.createContext<string[]>(["*"]);

export function useCan(): (p: string) => boolean {
  const perms = React.useContext(PermsContext);
  return (p) => perms.includes("*") || perms.includes(p);
}
