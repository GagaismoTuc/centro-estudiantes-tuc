import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import { fail, me, mockApi, ok, sentTo, type Reply } from "./test/api.ts";

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
  state: "closed",
  commission: [ana],
};

/** A student at the closed election; `results` answers GET /elections/e1/results. */
function closedElection(results: Reply | Reply[], state = "closed") {
  const sent = mockApi({
    "GET /auth/me": ok(me({ role: "student", mfa: "not_required" })),
    "GET /electoral-commission": ok({ commission: null, isMember: false, canDesignate: false }),
    "GET /elections/latest": ok({ ...election, state }),
    "GET /elections/e1/lists": ok({ rosterMemberId: "r1", lists: [] }),
    "GET /elections/e1/results": results,
  });
  window.history.replaceState(null, "", "/eleccion");
  render(<App />);
  return sent;
}

const published = (rest: object) => ok({ status: "published", mode: "secret", ...rest });

/** The rows of the results table as "name: votes". */
async function rows() {
  const table = await screen.findByRole("table", { name: "Votos por lista" });
  return within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) => [...(r as HTMLTableRowElement).cells].map((c) => c.textContent).join(": "));
}

test("S12: the winner is named, with each list and its votes, Voto en blanco and Participación: 130 de 200", async () => {
  closedElection(
    published({
      lists: [
        { id: "l1", name: "Lista Azul", votes: 70 },
        { id: "l2", name: "Lista Verde", votes: 50 },
      ],
      blankVotes: 10,
      voted: 130,
      electorate: 200,
      outcome: "winner",
      winner: { id: "l1", name: "Lista Azul" },
    }),
  );
  expect(await screen.findByRole("heading", { level: 2, name: "Resultado" })).toBeTruthy();
  expect(await rows()).toEqual(["Lista Azul: 70", "Lista Verde: 50", "Voto en blanco: 10"]);
  expect(screen.getByText("Participación: 130 de 200")).toBeTruthy();
  expect(screen.getByText("Ganó Lista Azul")).toBeTruthy();
  expect(screen.queryByText(/Sin ganador/)).toBeNull();
  // The mode shows once, in the election details.
  expect(screen.getAllByText("Voto secreto")).toHaveLength(1);
});

test("S12: a tie says Sin ganador: empate and names no winner", async () => {
  closedElection(
    published({
      lists: [
        { id: "l1", name: "Lista Azul", votes: 40 },
        { id: "l2", name: "Lista Verde", votes: 40 },
        { id: "l3", name: "Lista Roja", votes: 10 },
      ],
      blankVotes: 0,
      voted: 90,
      electorate: 200,
      outcome: "tie",
    }),
  );
  expect(await screen.findByText("Sin ganador: empate")).toBeTruthy();
  expect(await rows()).toEqual(["Lista Azul: 40", "Lista Verde: 40", "Lista Roja: 10", "Voto en blanco: 0"]);
  expect(screen.getByText("Participación: 90 de 200")).toBeTruthy();
  expect(screen.queryByText(/^Ganó/)).toBeNull();
});

test("S12: with no votes for any list it says Sin ganador, even with blank votes", async () => {
  closedElection(
    published({
      lists: [{ id: "l1", name: "Lista Azul", votes: 0 }],
      blankVotes: 5,
      voted: 5,
      electorate: 200,
      outcome: "no_votes",
    }),
  );
  const outcome = await screen.findByText("Sin ganador");
  expect(outcome.textContent).toBe("Sin ganador");
  expect(await rows()).toEqual(["Lista Azul: 0", "Voto en blanco: 5"]);
  expect(screen.getByText("Participación: 5 de 200")).toBeTruthy();
  expect(screen.queryByText(/^Ganó/)).toBeNull();
});

test("S12: with no approved lists it says Sin ganador: no hubo listas aprobadas and Participación: 0 de Y", async () => {
  closedElection(published({ lists: [], blankVotes: 0, voted: 0, electorate: 200, outcome: "no_lists" }));
  expect(await screen.findByText("Sin ganador: no hubo listas aprobadas")).toBeTruthy();
  expect(screen.getByText("Participación: 0 de 200")).toBeTruthy();
  expect(screen.queryByRole("table", { name: "Votos por lista" })).toBeNull();
  expect(screen.queryByText(/^Ganó/)).toBeNull();
});

test("S12: when the result cannot load, the error says so in voseo and Reintentar loads it", async () => {
  closedElection([
    fail(409, "results_not_available"),
    published({
      lists: [{ id: "l1", name: "Lista Azul", votes: 3 }],
      blankVotes: 10,
      voted: 13,
      electorate: 200,
      outcome: "winner",
      winner: { id: "l1", name: "Lista Azul" },
    }),
  ]);
  expect(await screen.findByText("Los resultados se ven cuando cierra la elección.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Reintentar" }));
  expect(await screen.findByText("Ganó Lista Azul")).toBeTruthy();
  expect(screen.queryByText("Los resultados se ven cuando cierra la elección.")).toBeNull();
  expect(screen.queryByRole("button", { name: "Reintentar" })).toBeNull();
  // The button that held focus is gone: focus goes to the outcome.
  await waitFor(() => expect(document.activeElement?.textContent).toBe("Ganó Lista Azul"));
});

test("S12: an annulled election shows no result and does not ask for it", async () => {
  const sent = closedElection(fail(409, "results_not_available"), "annulled");
  await screen.findByRole("heading", { level: 2, name: "Elección 2027" });
  expect(screen.queryByRole("heading", { level: 2, name: "Resultado" })).toBeNull();
  expect(sentTo(sent, "GET /elections/e1/results")).toHaveLength(0);
});
