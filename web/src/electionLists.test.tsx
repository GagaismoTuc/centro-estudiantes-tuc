import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import css from "./styles.css?raw";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

const ana = { rosterMemberId: "r-ana", fullName: "Ana Ficticia" };
const beto = { rosterMemberId: "r-beto", fullName: "Beto Ficticio" };
const ciro = { rosterMemberId: "r-ciro", fullName: "Ciro Inventado" };
const dana = { rosterMemberId: "r-dana", fullName: "Dana Ejemplo" };
const eli = { rosterMemberId: "r-eli", fullName: "Eli Supuesto" };
const fito = { rosterMemberId: "r-fito", fullName: "Fito Cuarto" };

const allCandidates = [ana, beto, ciro, dana, eli, fito];

const submissionElection = {
  id: "e1",
  name: "Elección 2027",
  mode: "secret" as const,
  submissionStartsAt: "2027-03-01T03:00:00Z",
  submissionEndsAt: "2027-03-06T03:00:00Z",
  startsAt: "2027-03-10T11:00:00Z",
  endsAt: "2027-03-10T21:00:00Z",
  positions: ["Presidente", "Vicepresidente", "Secretario", "Tesorero"],
  state: "submission" as const,
  commission: [ana, beto],
};

function renderAt(url: string) {
  window.history.replaceState(null, "", url);
  return render(<App />);
}

function everyText(): string {
  const attrs = Array.from(document.querySelectorAll("[aria-label], [title], [placeholder], [alt]")).flatMap((el) =>
    ["aria-label", "title", "placeholder", "alt"].map((a) => el.getAttribute(a) ?? ""),
  );
  return [document.title, document.body.textContent ?? "", ...attrs].join("\n");
}

test("R13/R1/CS-9: student presents a list with voseo, validation focuses first invalid field, and no Junta text", async () => {
  const created = {
    id: "l1",
    electionId: "e1",
    name: "Lista Azul",
    status: "pending",
    rejectionReason: null,
    representativeRosterMemberId: "r-ciro",
    representativeFullName: "Ciro Inventado",
    candidates: [
      { position: "Presidente", rosterMemberId: "r-ciro", fullName: "Ciro Inventado" },
      { position: "Vicepresidente", rosterMemberId: "r-dana", fullName: "Dana Ejemplo" },
      { position: "Secretario", rosterMemberId: "r-eli", fullName: "Eli Supuesto" },
      { position: "Tesorero", rosterMemberId: "r-fito", fullName: "Fito Cuarto" },
    ],
    reviewedAt: null,
    createdAt: "2027-03-01T12:00:00Z",
    updatedAt: "2027-03-01T12:00:00Z",
  };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
    "GET /electoral-commission": ok({ commission: { id: "c1", createdAt: "2026-11-01T12:00:00Z", members: [ana, beto] }, isMember: false, canDesignate: false }),
    "GET /elections/current": ok(submissionElection),
    "GET /elections/e1/candidates": ok(allCandidates),
    "GET /elections/e1/lists": [ok({ rosterMemberId: "r-ciro", lists: [] }), ok({ rosterMemberId: "r-ciro", lists: [created] })],
    "POST /elections/e1/lists": ok(created, 201),
  });

  renderAt("/eleccion");
  expect(await screen.findByRole("heading", { level: 2, name: "Presentá tu lista" })).toBeTruthy();
  expect(screen.getByText("Elegí un candidato para cada cargo")).toBeTruthy();
  expect(everyText()).not.toMatch(/junta/i);

  const nameInput = screen.getByLabelText("Nombre de la lista") as HTMLInputElement;
  expect(nameInput.closest("form")?.noValidate).toBe(true);

  // Empty submission marks and focuses the list name input.
  fireEvent.click(screen.getByRole("button", { name: "Presentar lista" }));
  await waitFor(() => expect(nameInput.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(nameInput);
  expect(nameInput.getAttribute("aria-describedby")).toBeTruthy();

  fireEvent.change(nameInput, { target: { value: "Lista Azul" } });
  fireEvent.change(screen.getByLabelText("Presidente"), { target: { value: "r-ciro" } });
  fireEvent.change(screen.getByLabelText("Vicepresidente"), { target: { value: "r-dana" } });
  fireEvent.change(screen.getByLabelText("Secretario"), { target: { value: "r-eli" } });
  fireEvent.change(screen.getByLabelText("Tesorero"), { target: { value: "r-fito" } });
  fireEvent.click(screen.getByRole("button", { name: "Presentar lista" }));

  await waitFor(() => expect(sentTo(sent, "POST /elections/e1/lists")).toHaveLength(1));
  expect(sentTo(sent, "POST /elections/e1/lists")[0]!.body).toEqual({
    name: "Lista Azul",
    candidates: [
      { position: "Presidente", rosterMemberId: "r-ciro" },
      { position: "Vicepresidente", rosterMemberId: "r-dana" },
      { position: "Secretario", rosterMemberId: "r-eli" },
      { position: "Tesorero", rosterMemberId: "r-fito" },
    ],
  });
  expect(await screen.findByText("Se presentó tu lista.")).toBeTruthy();
  expect(await screen.findByText("Pendiente")).toBeTruthy();
  expect(screen.getByText(/Apoderado:/)).toBeTruthy();
});

test("R11/R9: student sees approved lists, rejected list with reason, and edits pending list", async () => {
  const rejected = {
    id: "l-rej",
    electionId: "e1",
    name: "Lista Roja",
    status: "rejected",
    rejectionReason: "Falta aval del curso",
    representativeRosterMemberId: "r-ciro",
    representativeFullName: "Ciro Inventado",
    candidates: [
      { position: "Presidente", rosterMemberId: "r-ciro", fullName: "Ciro Inventado" },
      { position: "Vicepresidente", rosterMemberId: "r-dana", fullName: "Dana Ejemplo" },
      { position: "Secretario", rosterMemberId: "r-eli", fullName: "Eli Supuesto" },
      { position: "Tesorero", rosterMemberId: "r-fito", fullName: "Fito Cuarto" },
    ],
    reviewedAt: "2027-03-01T14:00:00Z",
    createdAt: "2027-03-01T12:00:00Z",
    updatedAt: "2027-03-01T14:00:00Z",
  };
  const pending = { ...rejected, id: "l-pen", name: "Lista Azul", status: "pending", rejectionReason: null, reviewedAt: null };
  const approved = {
    ...rejected,
    id: "l-app",
    name: "Lista Verde",
    status: "approved",
    rejectionReason: null,
    representativeRosterMemberId: "r-dana",
    representativeFullName: "Dana Ejemplo",
  };

  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
    "GET /electoral-commission": ok({ commission: { id: "c1", createdAt: "2026-11-01T12:00:00Z", members: [ana, beto] }, isMember: false, canDesignate: false }),
    "GET /elections/current": ok(submissionElection),
    "GET /elections/e1/candidates": ok(allCandidates),
    "GET /elections/e1/lists": ok({ rosterMemberId: "r-ciro", lists: [rejected, pending, approved] }),
    "PUT /elections/e1/lists/l-pen": ok({ ...pending, name: "Lista Celeste" }),
  });

  renderAt("/eleccion");
  expect(await screen.findByText("Falta aval del curso")).toBeTruthy();
  expect(screen.getByRole("heading", { level: 2, name: "Listas aprobadas" })).toBeTruthy();
  expect(screen.getByText("Lista Verde")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Editar lista Lista Azul" }));
  const nameInput = screen.getByLabelText("Nombre de la lista") as HTMLInputElement;
  expect(nameInput.value).toBe("Lista Azul");
  fireEvent.change(nameInput, { target: { value: "Lista Celeste" } });
  fireEvent.click(screen.getByRole("button", { name: "Guardar cambios" }));

  await waitFor(() => expect(sentTo(sent, "PUT /elections/e1/lists/l-pen")).toHaveLength(1));
  expect(await screen.findByText("Se guardaron los cambios de la lista.")).toBeTruthy();
});

test("R2/R8/R13: commission member approves, rejects with reason validation, and loads list for Apoderado", async () => {
  const pending1 = {
    id: "l1",
    electionId: "e1",
    name: "Lista Azul",
    status: "pending",
    rejectionReason: null,
    representativeRosterMemberId: "r-ciro",
    representativeFullName: "Ciro Inventado",
    candidates: [
      { position: "Presidente", rosterMemberId: "r-ciro", fullName: "Ciro Inventado" },
      { position: "Vicepresidente", rosterMemberId: "r-dana", fullName: "Dana Ejemplo" },
      { position: "Secretario", rosterMemberId: "r-eli", fullName: "Eli Supuesto" },
      { position: "Tesorero", rosterMemberId: "r-fito", fullName: "Fito Cuarto" },
    ],
    reviewedAt: null,
    createdAt: "2027-03-01T12:00:00Z",
    updatedAt: "2027-03-01T12:00:00Z",
  };
  const pending2 = { ...pending1, id: "l2", name: "Lista Blanca" };
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "satisfied" })),
    "GET /electoral-commission": ok({ commission: { id: "c1", createdAt: "2026-11-01T12:00:00Z", members: [ana, beto] }, isMember: true, canDesignate: false }),
    "GET /elections/current": ok(submissionElection),
    "GET /elections/e1/candidates": ok(allCandidates),
    "GET /elections/e1/lists": ok({ rosterMemberId: "r-ana", lists: [pending1, pending2] }),
    "POST /elections/e1/lists/l1/approve": ok({ ...pending1, status: "approved" }),
    "POST /elections/e1/lists/l2/reject": ok({ ...pending2, status: "rejected", rejectionReason: "Cargo repetido" }),
    "POST /elections/e1/commission/lists": ok({ ...pending1, id: "l3", name: "Lista Violeta" }, 201),
  });

  renderAt("/centro-de-estudiantes");
  expect(await screen.findByRole("heading", { level: 1, name: "Centro de estudiantes" })).toBeTruthy();
  expect(await screen.findByRole("heading", { level: 2, name: "Listas presentadas" })).toBeTruthy();
  expect(everyText()).not.toMatch(/junta/i);

  // Approve l1.
  fireEvent.click(screen.getByRole("button", { name: "Aprobar Lista Azul" }));
  await waitFor(() => expect(sentTo(sent, "POST /elections/e1/lists/l1/approve")).toHaveLength(1));
  expect(await screen.findByText("Se aprobó la lista.")).toBeTruthy();

  // Reject l2: empty reason fails client validation, focuses textarea.
  fireEvent.click(screen.getByRole("button", { name: "Rechazar Lista Blanca" }));
  const reasonInput = await screen.findByLabelText("Motivo del rechazo");
  fireEvent.click(screen.getByRole("button", { name: "Confirmar rechazo" }));
  await waitFor(() => expect(reasonInput.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(reasonInput);

  fireEvent.change(reasonInput, { target: { value: "Cargo repetido" } });
  fireEvent.click(screen.getByRole("button", { name: "Confirmar rechazo" }));
  await waitFor(() => expect(sentTo(sent, "POST /elections/e1/lists/l2/reject")).toHaveLength(1));
  expect(sentTo(sent, "POST /elections/e1/lists/l2/reject")[0]!.body).toEqual({ reason: "Cargo repetido" });

  // Commission loads list on behalf of Apoderado.
  const loadSection = screen.getByRole("region", { name: "Cargá una lista" });
  fireEvent.change(within(loadSection).getByLabelText("Apoderado"), { target: { value: "r-ciro" } });
  fireEvent.change(within(loadSection).getByLabelText("Nombre de la lista"), { target: { value: "Lista Violeta" } });
  fireEvent.change(within(loadSection).getByLabelText("Presidente"), { target: { value: "r-ciro" } });
  fireEvent.change(within(loadSection).getByLabelText("Vicepresidente"), { target: { value: "r-dana" } });
  fireEvent.change(within(loadSection).getByLabelText("Secretario"), { target: { value: "r-eli" } });
  fireEvent.change(within(loadSection).getByLabelText("Tesorero"), { target: { value: "r-fito" } });
  fireEvent.click(within(loadSection).getByRole("button", { name: "Cargar lista" }));

  await waitFor(() => expect(sentTo(sent, "POST /elections/e1/commission/lists")).toHaveLength(1));
  expect(sentTo(sent, "POST /elections/e1/commission/lists")[0]!.body).toMatchObject({
    name: "Lista Violeta",
    representativeRosterMemberId: "r-ciro",
  });
});

test("CS-9/CS-11: list_name_taken focuses list name and list grid uses minmax(0, 1fr)", async () => {
  mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
    "GET /electoral-commission": ok({ commission: { id: "c1", createdAt: "2026-11-01T12:00:00Z", members: [ana, beto] }, isMember: false, canDesignate: false }),
    "GET /elections/current": ok(submissionElection),
    "GET /elections/e1/candidates": ok(allCandidates),
    "GET /elections/e1/lists": ok({ rosterMemberId: "r-ciro", lists: [] }),
    "POST /elections/e1/lists": fail(409, "list_name_taken"),
  });

  renderAt("/eleccion");
  const nameInput = await screen.findByLabelText("Nombre de la lista");
  fireEvent.change(nameInput, { target: { value: "Lista Azul" } });
  fireEvent.change(screen.getByLabelText("Presidente"), { target: { value: "r-ciro" } });
  fireEvent.change(screen.getByLabelText("Vicepresidente"), { target: { value: "r-dana" } });
  fireEvent.change(screen.getByLabelText("Secretario"), { target: { value: "r-eli" } });
  fireEvent.change(screen.getByLabelText("Tesorero"), { target: { value: "r-fito" } });
  fireEvent.click(screen.getByRole("button", { name: "Presentar lista" }));

  await waitFor(() => expect(nameInput.getAttribute("aria-invalid")).toBe("true"));
  expect(document.activeElement).toBe(nameInput);
  expect(screen.getByText("Ya hay otra lista con ese nombre en esta elección.")).toBeTruthy();

  const rule = (selector: string) =>
    [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)]
      .filter((m) => m[1]!.split(",").some((s) => s.trim() === selector))
      .map((m) => m[2])
      .join("\n");
  expect(rule(".election-lists")).toContain("minmax(0, 1fr)");
  expect(rule(".list-card__title")).toContain("overflow-wrap: anywhere");
});

test("CS-9: CommissionPanel moves focus to FormStatus when approve fails", async () => {
  const pending = {
    id: "l1",
    electionId: "e1",
    name: "Lista Azul",
    status: "pending",
    rejectionReason: null,
    representativeRosterMemberId: "r-ciro",
    representativeFullName: "Ciro Inventado",
    candidates: [
      { position: "Presidente", rosterMemberId: "r-ciro", fullName: "Ciro Inventado" },
      { position: "Vicepresidente", rosterMemberId: "r-dana", fullName: "Dana Ejemplo" },
      { position: "Secretario", rosterMemberId: "r-eli", fullName: "Eli Supuesto" },
      { position: "Tesorero", rosterMemberId: "r-fito", fullName: "Fito Cuarto" },
    ],
    reviewedAt: null,
    createdAt: "2027-03-01T12:00:00Z",
    updatedAt: "2027-03-01T12:00:00Z",
  };
  mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "satisfied" })),
    "GET /electoral-commission": ok({ commission: { id: "c1", createdAt: "2026-11-01T12:00:00Z", members: [ana, beto] }, isMember: true, canDesignate: false }),
    "GET /elections/current": ok(submissionElection),
    "GET /elections/e1/candidates": ok(allCandidates),
    "GET /elections/e1/lists": ok({ rosterMemberId: "r-ana", lists: [pending] }),
    "POST /elections/e1/lists/l1/approve": fail(409, "submission_closed"),
  });

  renderAt("/centro-de-estudiantes");
  fireEvent.click(await screen.findByRole("button", { name: "Aprobar Lista Azul" }));
  const msg = await screen.findByText("El período de presentación de listas no está abierto.");
  const statusRegion = msg.closest("[tabindex='-1']");
  await waitFor(() => expect(document.activeElement).toBe(statusRegion));
});
