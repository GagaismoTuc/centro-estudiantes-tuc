import { useEffect, useState } from "react";
import { apiClient } from "./api/client.ts";

type ApiStatus = "loading" | "ok" | "offline";

const statusLabel: Record<ApiStatus, string> = {
  loading: "API: comprobando…",
  ok: "API: ok",
  offline: "API: sin conexión",
};

async function checkHealth(): Promise<ApiStatus> {
  try {
    const { data } = await apiClient.GET("/health");
    return data?.status === "ok" ? "ok" : "offline";
  } catch {
    return "offline";
  }
}

export function App() {
  const [status, setStatus] = useState<ApiStatus>("loading");

  useEffect(() => {
    let active = true;
    void checkHealth().then((next) => {
      if (active) setStatus(next);
    });
    return () => {
      active = false;
    };
  }, []);

  return (
    <main className="page">
      <h1 className="page__title">Centro de Estudiantes</h1>
      <p className="status" data-status={status} role="status">
        {statusLabel[status]}
      </p>
    </main>
  );
}
