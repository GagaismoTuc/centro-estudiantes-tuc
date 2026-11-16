import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import css from "./styles.css?raw";
import { emulateDialog, fail, me, mockApi, ok, sentTo, type Reply } from "./test/api.ts";

let restoreDialog: () => void;
beforeEach(() => {
  restoreDialog = emulateDialog();
});
afterEach(() => {
  restoreDialog();
  vi.restoreAllMocks();
});

const ana = { rosterMemberId: "r1", fullName: "Ana Ficticia" };
const beto = { rosterMemberId: "r2", fullName: "Beto Ficticio" };

function status(overrides: { isMember?: boolean; canDesignate?: boolean; members?: (typeof ana)[] | null } = {}): Reply {
  const members = overrides.members === undefined ? [ana] : overrides.members;
  return ok({
    commission: members === null ? null : { id: "c1", createdAt: "2026-11-01T12:00:00Z", members },
    isMember: overrides.isMember ?? false,
    canDesignate: overrides.canDesignate ?? false,
  });
}

const election = {
  id: "e1",
  name: "Elección 2027",
  mode: "secret",
  submissionStartsAt: "2027-03-01T03:00:00Z",
  submissionEndsAt: "2027-03-06T03:00:00Z",
  startsAt: "2027-03-10T11:00:00Z",
  endsAt: "2027-03-10T21:00:00Z",
  positions: ["Presidente", "Vicepresidente", "Secretario", "Tesorero", "Vocal"],
  state: "scheduled",
  commission: [ana, beto],
};

function renderAt(url: string) {
  window.history.replaceState(null, "", url);
  return render(<App />);
}

const mainNav = () => screen.getAllByRole("navigation", { name: "Principal" })[0] as HTMLElement;

/** Visible text plus every label-like attribute on the page. */
function everyText(): string {
  const attrs = Array.from(document.querySelectorAll("[aria-label], [title], [placeholder], [alt]")).flatMap((el) =>
    ["aria-label", "title", "placeholder", "alt"].map((a) => el.getAttribute(a) ?? ""),
  );
  return [document.title, document.body.textContent ?? "", ...attrs].join("\n");
}

test("S13: the commission is labeled Centro de estudiantes, texts use voseo and never say Junta", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "officer", mfa: "satisfied" })),
    "GET /electoral-commission": status({ isMember: true, canDesignate: true }),
    "GET /electoral-commission/candidates": ok([ana, beto]),
    "GET /elections/latest": fail(404, "not_found"),
    "GET /elections/current": fail(404, "not_found"),
  });
  renderAt("/eleccion");
  expect(await screen.findByRole("heading", { level: 1, name: "Elección" })).toBeTruthy();
  expect(await screen.findByText("Elegí los integrantes")).toBeTruthy();
  expect(screen.getByText("Todavía no hay una elección en curso.")).toBeTruthy();
  const link = await within(mainNav()).findByRole("link", { name: "Centro de estudiantes" });
  expect(everyText()).not.toMatch(/junta/i);

  fireEvent.click(link);
  expect(await screen.findByRole("heading", { level: 1, name: "Centro de estudiantes" })).toBeTruthy();
  expect(await screen.findByRole("heading", { level: 2, name: "Creá la elección" })).toBeTruthy();
  expect(screen.getByText("Voto secreto")).toBeTruthy();
  expect(everyText()).not.toMatch(/junta/i);
});

test("a commission member creates the draft with the default positions in Argentina time", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "satisfied" })),
    "GET /electoral-commission": status({ isMember: true }),
    "GET /elections/current": fail(404, "not_found"),
    "POST /elections": ok({ ...election, state: "draft", positions: ["Presidente", "Vicepresidente", "Secretario", "Tesorero"] }, 201),
  });
  renderAt("/centro-de-estudiantes");
  await screen.findByRole("heading", { level: 2, name: "Creá la elección" });
  const positions = screen.getAllByRole("textbox", { name: /^Cargo \d$/ }) as HTMLInputElement[];
  expect(positions.map((p) => p.value)).toEqual(["Presidente", "Vicepresidente", "Secretario", "Tesorero"]);
  const requiredFields = [
    "Nombre de la elección",
    "Inicio de la presentación de listas",
    "Fin de la presentación de listas",
    "Inicio de la votación",
    "Fin de la votación",
  ];
  for (const label of requiredFields) expect((screen.getByLabelText(label) as HTMLInputElement).required).toBe(true);
  expect(screen.getByLabelText("Nombre de la elección").closest("form")?.noValidate).toBe(true);

  fireEvent.change(screen.getByLabelText("Nombre de la elección"), { target: { value: "Elección 2027" } });
  fireEvent.change(screen.getByLabelText("Inicio de la presentación de listas"), { target: { value: "2027-03-01T00:00" } });
  fireEvent.change(screen.getByLabelText("Fin de la presentación de listas"), { target: { value: "2027-03-06T00:00" } });
  fireEvent.change(screen.getByLabelText("Inicio de la votación"), { target: { value: "2027-03-10T08:00" } });
  fireEvent.change(screen.getByLabelText("Fin de la votación"), { target: { value: "2027-03-10T18:00" } });
  fireEvent.click(screen.getByRole("button", { name: "Crear elección" }));

  await waitFor(() => expect(sentTo(sent, "POST /elections")).toHaveLength(1));
  expect(sentTo(sent, "POST /elections")[0]!.body).toEqual({
    name: "Elección 2027",
    mode: "secret",
    submissionStartsAt: "2027-03-01T00:00:00-03:00",
    submissionEndsAt: "2027-03-06T00:00:00-03:00",
    startsAt: "2027-03-10T08:00:00-03:00",
    endsAt: "2027-03-10T18:00:00-03:00",
    positions: ["Presidente", "Vicepresidente", "Secretario", "Tesorero"],
  });
  expect(await screen.findByText("Se creó la elección.")).toBeTruthy();
  expect(await screen.findByRole("heading", { level: 2, name: "Editá la elección" })).toBeTruthy();
});

test("the commission edits positions of the draft and a 422 marks and focuses the fields it is about", async () => {
  const draft = { ...election, state: "draft" };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "satisfied" })),
    "GET /electoral-commission": status({ isMember: true }),
    "GET /elections/current": ok(draft),
    "PUT /elections/e1": [fail(422, "invalid_window"), ok(draft)],
  });
  renderAt("/centro-de-estudiantes");
  await screen.findByRole("heading", { level: 2, name: "Editá la elección" });
  expect((screen.getByLabelText("Inicio de la votación") as HTMLInputElement).value).toBe("2027-03-10T08:00");
  fireEvent.click(screen.getByRole("button", { name: "Quitar el cargo Vocal" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Cargo 4" }), { target: { value: "Tesorera" } });
  fireEvent.click(screen.getByRole("button", { name: "Agregar cargo" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Cargo 5" }), { target: { value: "Vocal" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar cambios" }));

  const startsAt = screen.getByLabelText("Inicio de la votación");
  await waitFor(() => expect(startsAt.getAttribute("aria-invalid")).toBe("true"));
  expect(screen.getByLabelText("Fin de la votación").getAttribute("aria-invalid")).toBe("true");
  expect(document.activeElement).toBe(startsAt);
  expect(screen.getByText(/Revisá las fechas de votación/)).toBeTruthy();
  expect(sentTo(sent, "PUT /elections/e1")[0]!.body).toMatchObject({
    positions: ["Presidente", "Vicepresidente", "Secretario", "Tesorera", "Vocal"],
    startsAt: "2027-03-10T08:00:00-03:00",
  });
});

test("whoever can designate picks the members of the Centro de estudiantes and saves them", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "school_admin", mfa: "satisfied" })),
    "GET /electoral-commission": [status({ canDesignate: true }), status({ canDesignate: true, members: [ana, beto] })],
    "GET /electoral-commission/candidates": ok([ana, beto]),
    "GET /elections/latest": fail(404, "not_found"),
    "PUT /electoral-commission": [fail(409, "election_frozen"), ok({ id: "c2", createdAt: "2026-11-15T12:00:00Z", members: [ana, beto] })],
  });
  renderAt("/eleccion");
  const anaBox = (await screen.findByRole("checkbox", { name: "Ana Ficticia" })) as HTMLInputElement;
  expect(anaBox.checked).toBe(true);
  fireEvent.click(screen.getByRole("checkbox", { name: "Beto Ficticio" }));
  fireEvent.click(screen.getByRole("button", { name: "Guardar integrantes" }));

  expect(await screen.findByText("La elección ya abrió: no se puede cambiar el Centro de estudiantes.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Guardar integrantes" }));
  expect(await screen.findByText("Se guardó el Centro de estudiantes.")).toBeTruthy();
  expect(sentTo(sent, "PUT /electoral-commission").map((s) => s.body)).toEqual([
    { rosterMemberIds: ["r1", "r2"] },
    { rosterMemberIds: ["r1", "r2"] },
  ]);
});

test("a student sees the election with its Spanish state, dates, positions and commission, and no commission menu", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
    "GET /electoral-commission": status({ members: [ana, beto] }),
    "GET /elections/latest": ok(election),
  });
  renderAt("/");
  await screen.findAllByRole("navigation", { name: "Principal" });
  fireEvent.click(await within(mainNav()).findByRole("link", { name: "Elección" }));
  expect(await screen.findByRole("heading", { level: 2, name: "Elección 2027" })).toBeTruthy();
  expect(screen.getByText("Programada")).toBeTruthy();
  expect(screen.getByText("10 de marzo de 2027 a las 08:00")).toBeTruthy();
  expect(screen.getByText("10 de marzo de 2027 a las 18:00")).toBeTruthy();
  const positions = screen.getByRole("list", { name: "Cargos" });
  expect(within(positions).getAllByRole("listitem").map((li) => li.textContent)).toEqual(election.positions);
  const commission = screen.getByRole("list", { name: "Integrantes del Centro de estudiantes" });
  expect(within(commission).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["Ana Ficticia", "Beto Ficticio"]);
  expect(screen.queryByText("Elegí los integrantes")).toBeNull();
  expect(within(mainNav()).queryByRole("link", { name: "Centro de estudiantes" })).toBeNull();
});

test("long single-word names wrap at 360 px and checkboxes keep their size", () => {
  // Every block whose selector list names `selector`, joined.
  const rule = (selector: string) =>
    [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)]
      .filter((m) => m[1]!.split(",").some((s) => s.trim() === selector))
      .map((m) => m[2])
      .join("\n");
  expect(rule(".plain-list li")).toContain("overflow-wrap: anywhere");
  expect(rule(".check label")).toContain("overflow-wrap: anywhere");
  expect(rule(".check input")).toContain("flex: none");
  expect(rule(".check input")).toContain("inline-size: 1.25rem");
});

const reason = "Se detectó un error en el padrón cargado";
const annulled = { ...election, state: "annulled", annulment: { reason, annulledAt: "2027-03-10T12:00:00Z" } };

test("R11: the commission annuls the election after confirming with a reason it is asked for in voseo", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "satisfied" })),
    "GET /electoral-commission": [status({ isMember: true }), status({ members: null })],
    "GET /elections/current": [ok({ ...election, state: "open" }), fail(404, "not_found")],
    "POST /elections/e1/annulment": ok(annulled),
  });
  renderAt("/centro-de-estudiantes");
  fireEvent.click(await screen.findByRole("button", { name: "Anular elección" }));
  let dialog = await screen.findByRole("dialog", { name: "¿Anular la elección?" });
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancelar" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

  fireEvent.click(screen.getByRole("button", { name: "Anular elección" }));
  dialog = await screen.findByRole("dialog", { name: "¿Anular la elección?" });
  const field = within(dialog).getByLabelText("Motivo");
  expect(within(dialog).getByText("Escribí el motivo; lo van a ver todos.")).toBeTruthy();
  fireEvent.change(field, { target: { value: "   " } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Anular definitivamente" }));
  await waitFor(() => expect(field.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(field);
  expect(within(dialog).getByText("Escribí el motivo antes de anular la elección.")).toBeTruthy();
  expect(sentTo(sent, "POST /elections/e1/annulment")).toHaveLength(0);

  fireEvent.change(field, { target: { value: reason } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Anular definitivamente" }));
  await waitFor(() => expect(sentTo(sent, "POST /elections/e1/annulment")).toHaveLength(1));
  expect(sentTo(sent, "POST /elections/e1/annulment")[0]!.body).toEqual({ reason });
  expect(await screen.findByText("Se anuló la elección. Todos van a ver el motivo.")).toBeTruthy();
  await waitFor(() => expect(document.activeElement?.textContent).toBe("Se anuló la elección. Todos van a ver el motivo."));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.queryByRole("button", { name: "Anular elección" })).toBeNull();
});

test("R11: every member sees the election as Anulada with its reason", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
    "GET /electoral-commission": status({ members: [beto] }),
    "GET /elections/latest": ok(annulled),
  });
  renderAt("/eleccion");
  expect(await screen.findByRole("heading", { level: 2, name: "Elección 2027" })).toBeTruthy();
  expect(screen.getByText((_, el) => el?.tagName === "P" && el.textContent === `Anulada: ${reason}`)).toBeTruthy();
  expect(screen.getByText("Anulada")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Anular elección" })).toBeNull();
  // The commission the Officers designate next shows next to the annulled election.
  expect(screen.getByRole("heading", { level: 2, name: "Centro de estudiantes" })).toBeTruthy();
  const lists = screen.getAllByRole("list", { name: "Integrantes del Centro de estudiantes" });
  expect(lists.map((l) => within(l).getAllByRole("listitem").map((li) => li.textContent))).toEqual([["Ana Ficticia", "Beto Ficticio"], ["Beto Ficticio"]]);
});
