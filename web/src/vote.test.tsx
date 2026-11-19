import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, noContent, ok, sentTo, type Reply } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

const ana = { rosterMemberId: "r1", fullName: "Ana Ficticia" };

const election = {
  id: "e1",
  name: "Elección 2027",
  mode: "secret",
  submissionStartsAt: "2027-03-01T03:00:00Z",
  submissionEndsAt: "2027-03-06T03:00:00Z",
  startsAt: "2027-03-10T11:00:00Z",
  endsAt: "2027-03-10T21:00:00Z",
  positions: ["Presidente"],
  state: "open",
  commission: [ana],
};

function list(id: string, name: string, status: "approved" | "rejected" | "pending") {
  return {
    id,
    electionId: "e1",
    name,
    status,
    rejectionReason: null,
    representativeRosterMemberId: "r2",
    representativeFullName: "Beto Ficticio",
    candidates: [{ position: "Presidente", rosterMemberId: "r2", fullName: "Beto Ficticio" }],
    reviewedAt: null,
    createdAt: "2027-03-02T12:00:00Z",
    updatedAt: "2027-03-02T12:00:00Z",
  };
}

const GUARANTEE =
  "El voto es secreto para las autoridades de la escuela y para quienes administran la plataforma en la escuela. No es secreto para un operador con acceso root al servidor mientras la elección está abierta. No usamos esquemas criptográficos como firmas ciegas o mixnets.";

/**
 * Reads a doc outside web/ with Node's fs: Vite's fs.allow denies it to a `?raw` import,
 * and the web has no @types/node, so fs is typed here with the one call it needs. The base
 * URL goes through a variable so Vite does not rewrite `new URL(…, import.meta.url)` as an asset.
 */
function readSchoolDoc(path: string): string {
  type Fs = { readFileSync(file: URL, encoding: "utf8"): string };
  const { process } = globalThis as unknown as { process: { getBuiltinModule(id: "node:fs"): Fs } };
  const here = import.meta.url;
  return process.getBuiltinModule("node:fs").readFileSync(new URL(`../../${path}`, here), "utf8");
}

/** A student of the Electorate at the open election, with one approved and one rejected list. */
function openElection(routes: Record<string, Reply | Reply[]>) {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
    "GET /electoral-commission": ok({ commission: null, isMember: false, canDesignate: false }),
    "GET /elections/latest": ok(election),
    "GET /elections/e1/lists": ok({ rosterMemberId: "r1", lists: [list("l1", "Lista Azul", "approved"), list("l2", "Lista Roja", "rejected")] }),
    ...routes,
  });
  window.history.replaceState(null, "", "/eleccion");
  render(<App />);
  return sent;
}

const turnout = (voted: number, hasVoted: boolean) => ok({ voted, electorate: 10, hasVoted });
const ballot = () => screen.queryByRole("group", { name: "Elegí una lista o votá en blanco" });

async function pick(name: string) {
  const fieldset = await screen.findByRole("group", { name: "Elegí una lista o votá en blanco" });
  fireEvent.click(within(fieldset).getByRole("radio", { name }));
  fireEvent.click(screen.getByRole("button", { name: "Seguir" }));
  return screen.findByRole("heading", { name: "Confirmá tu voto" });
}

test("S16: a student votes for an approved list after confirming it, in voseo, and then sees Ya votaste", async () => {
  const sent = openElection({
    "GET /elections/e1/turnout": [turnout(3, false), turnout(4, true)],
    "POST /elections/e1/vote": noContent(),
  });
  const fieldset = await screen.findByRole("group", { name: "Elegí una lista o votá en blanco" });
  // Only approved lists, plus the blank vote.
  const radios = within(fieldset).getAllByRole("radio") as HTMLInputElement[];
  expect(radios.map((r) => r.labels?.[0]?.textContent)).toEqual(["Lista Azul", "En blanco"]);
  expect(screen.getByText("3 de 10 votaron")).toBeTruthy();

  // Seguir without a choice says what to do and focuses the first option, already described by the message.
  let describedAtFocus: string | null = null;
  radios[0]!.addEventListener("focus", () => (describedAtFocus = radios[0]!.getAttribute("aria-describedby")), { once: true });
  fireEvent.click(screen.getByRole("button", { name: "Seguir" }));
  const choiceError = await screen.findByText("Elegí una lista o votá en blanco para seguir.");
  await waitFor(() => expect(describedAtFocus).toBe(choiceError.closest("[id]")?.id));
  expect(radios.map((r) => r.getAttribute("aria-invalid"))).toEqual(["true", "true"]);
  expect(document.activeElement).toBe(radios[0]);
  expect(screen.queryByRole("heading", { name: "Confirmá tu voto" })).toBeNull();

  const confirm = await pick("Lista Azul");
  await waitFor(() => expect(document.activeElement).toBe(confirm));
  const step = confirm.closest("section") as HTMLElement;
  expect(within(step).getByText("Lista Azul")).toBeTruthy();
  fireEvent.click(within(step).getByRole("button", { name: "Confirmar" }));

  expect(await screen.findByText("Ya votaste")).toBeTruthy();
  expect(sentTo(sent, "POST /elections/e1/vote").map((s) => s.body)).toEqual([{ choice: "l1" }]);
  expect(await screen.findByText("4 de 10 votaron")).toBeTruthy();
  expect(ballot()).toBeNull();
  expect(screen.queryByRole("button", { name: "Confirmar" })).toBeNull();
  expect(document.activeElement?.textContent).toBe("Ya votaste");
});

test("S16: a blank vote sends blank, and Volver goes back to the choice", async () => {
  const sent = openElection({
    "GET /elections/e1/turnout": [turnout(3, false), turnout(4, true)],
    "POST /elections/e1/vote": noContent(),
  });
  await pick("En blanco");
  fireEvent.click(screen.getByRole("button", { name: "Volver" }));
  const blank = (await screen.findByRole("radio", { name: "En blanco" })) as HTMLInputElement;
  expect(blank.checked).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(blank));
  fireEvent.click(screen.getByRole("button", { name: "Seguir" }));
  const step = (await screen.findByRole("heading", { name: "Confirmá tu voto" })).closest("section") as HTMLElement;
  expect(within(step).getByText("En blanco")).toBeTruthy();
  fireEvent.click(within(step).getByRole("button", { name: "Confirmar" }));

  expect(await screen.findByText("Ya votaste")).toBeTruthy();
  expect(sentTo(sent, "POST /elections/e1/vote").map((s) => s.body)).toEqual([{ choice: "blank" }]);
});

test("S16: retrying after the signal dropped answers 409 already_voted and the screen says Ya votaste without the form", async () => {
  openElection({
    "GET /elections/e1/turnout": [turnout(3, false), turnout(4, true)],
    "POST /elections/e1/vote": fail(409, "already_voted"),
  });
  await pick("Lista Azul");
  fireEvent.click(screen.getByRole("button", { name: "Confirmar" }));

  expect(await screen.findByText("Ya votaste")).toBeTruthy();
  expect(ballot()).toBeNull();
  expect(screen.queryByRole("button", { name: "Confirmar" })).toBeNull();
  expect(await screen.findByText("4 de 10 votaron")).toBeTruthy();
});

test("S12/S16: the turnout shows X de Y votaron and a student who already voted sees Ya votaste and no form", async () => {
  const sent = openElection({ "GET /elections/e1/turnout": turnout(7, true) });
  expect(await screen.findByText("7 de 10 votaron")).toBeTruthy();
  expect(screen.getByText("Ya votaste")).toBeTruthy();
  expect(ballot()).toBeNull();
  expect(screen.queryByRole("radio", { name: "Lista Azul" })).toBeNull();
  expect(sentTo(sent, "POST /elections/e1/vote")).toHaveLength(0);
});

test("S16: another error shows its message and keeps the choice so retrying is one tap", async () => {
  const sent = openElection({
    "GET /elections/e1/turnout": turnout(3, false),
    "POST /elections/e1/vote": fail(403, "not_in_electorate"),
  });
  await pick("Lista Azul");
  fireEvent.click(screen.getByRole("button", { name: "Confirmar" }));

  const message = await screen.findByText("No estás en el padrón electoral de esta elección.");
  await waitFor(() => expect(document.activeElement?.contains(message)).toBe(true));
  expect(screen.queryByText("Ya votaste")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Confirmar" }));
  await waitFor(() => expect(sentTo(sent, "POST /elections/e1/vote").map((s) => s.body)).toEqual([{ choice: "l1" }, { choice: "l1" }]));
  fireEvent.click(screen.getByRole("button", { name: "Volver" }));
  expect(((await screen.findByRole("radio", { name: "Lista Azul" })) as HTMLInputElement).checked).toBe(true);
});

test("S16: when the turnout cannot load, Reintentar loads it again and shows the ballot", async () => {
  openElection({ "GET /elections/e1/turnout": turnout(3, false) });
  // The first turnout request finds no signal.
  const answer = vi.mocked(fetch).getMockImplementation()!;
  let dropped = false;
  vi.mocked(fetch).mockImplementation(async (input) => {
    if (!dropped && new URL((input as Request).url).pathname.endsWith("/turnout")) {
      dropped = true;
      throw new TypeError("Failed to fetch");
    }
    return answer(input);
  });

  expect(await screen.findByText("No pudimos conectarnos. Revisá tu conexión e intentá de nuevo.")).toBeTruthy();
  expect(ballot()).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Reintentar" }));

  expect(await screen.findByRole("group", { name: "Elegí una lista o votá en blanco" })).toBeTruthy();
  expect(screen.getByText("3 de 10 votaron")).toBeTruthy();
  expect(screen.queryByText("No pudimos conectarnos. Revisá tu conexión e intentá de nuevo.")).toBeNull();
  expect(screen.queryByRole("button", { name: "Reintentar" })).toBeNull();
  // The button that held focus is gone: focus goes to the first option.
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("radio", { name: "Lista Azul" })));
  const turnoutRequests = vi.mocked(fetch).mock.calls.filter(([req]) => new URL((req as Request).url).pathname === "/api/elections/e1/turnout");
  expect(turnoutRequests).toHaveLength(2);
});

test("S13: the secret ballot guarantee shows on the Election screen, on Centro de estudiantes and in the school doc", async () => {
  const scheduled = { ...election, state: "scheduled" };
  mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "satisfied" })),
    "GET /electoral-commission": ok({ commission: { id: "c1", createdAt: "2026-11-01T12:00:00Z", members: [ana] }, isMember: true, canDesignate: false }),
    "GET /elections/latest": ok(scheduled),
    "GET /elections/current": ok(scheduled),
    "GET /elections/e1/lists": ok({ rosterMemberId: "r1", lists: [] }),
  });
  window.history.replaceState(null, "", "/eleccion");
  const { unmount } = render(<App />);
  expect(await screen.findByText(GUARANTEE)).toBeTruthy();
  unmount();

  window.history.replaceState(null, "", "/centro-de-estudiantes");
  render(<App />);
  await screen.findByRole("heading", { level: 1, name: "Centro de estudiantes" });
  expect(await screen.findByText(GUARANTEE)).toBeTruthy();

  expect(readSchoolDoc("docs/escuela/voto-secreto.md")).toContain(GUARANTEE);
});
