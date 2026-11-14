# Student centers (Centros de estudiantes)

A multi-tenant platform where secondary schools in Tucumán run their student center: its roster, its officers and its elections. Each term lists the Spanish word the UI shows beside it.

## Language

### Schools and people

**School** (UI: "Escuela"):
An educational institution that contracts the platform and is a Tenant.
_Avoid_: Institution, customer, account

**Tenant** (UI: not shown):
A customer whose data is isolated from every other customer's; today every Tenant is a School, later also a municipality.
_Avoid_: Organization, client, instance

**School admin** (UI: "Administrador de la escuela"):
An authority or staff member of a School who keeps the Roster and issues Access codes.
_Avoid_: Admin, director, platform admin

**Student** (UI: "Alumno"):
A person who uses the platform as a Roster member of a School.
_Avoid_: User, voter, member

**Roster** (UI: "Padrón"):
The School's list of its students, and the source of truth for who is a student there.
_Avoid_: Student list, census, enrollment

**Roster member** (UI: "Alumno del padrón"):
One entry of the Roster: a single student, whether or not they have claimed an account.
_Avoid_: Roster entry, enrollee, record

**Access code** (UI: "Código de acceso"):
A single-use code the School issues to one Roster member so that student can claim an account.
_Avoid_: Invitation, token, PIN

### Student center

**Student center** (UI: "Centro de estudiantes"):
The students' own organization within a School. The UI also shows "Centro de estudiantes" as the label of the Electoral commission; that label is display-only and never means the organization.
_Avoid_: Union, council, Junta

**Board** (UI: "Comisión directiva"):
The Officers of a Student center taken together.
_Avoid_: Committee, Junta, Electoral commission

**Officer** (UI: "Integrante de la comisión directiva"):
A Student who holds a Position on the Board.
_Avoid_: Authority, leader, delegate

**Position** (UI: "Cargo"):
A seat on the Board, defined per Election; by default Presidente, Vicepresidente, Secretario and Tesorero.
_Avoid_: Role, title, office

**Veedor** (UI: "Veedor"):
An optional impartial overseer appointed by the School, separate from the School admin, who watches an Election and can step in when the Student center is headless or in conflict.
_Avoid_: Auditor, observer, supervisor

### Elections

**Election** (UI: "Elección"):
A vote of the Electorate to choose which List fills the Board's Positions.
_Avoid_: Poll, survey, referendum

**Electoral commission** (UI: "Centro de estudiantes"):
The group of Roster members designated to create, configure and run one Election; its members cannot be Candidates. Its Spanish name is Comisión electoral; the UI shows it as "Centro de estudiantes".
_Avoid_: Junta, Junta electoral, Board

**Electorate** (UI: "Padrón electoral"):
The snapshot of the whole Roster taken when an Election opens; exactly these Roster members may vote in it.
_Avoid_: Eligible roster, voter list, census

**List** (UI: "Lista"):
A group of Candidates, one per Position, that competes in an Election as a whole.
_Avoid_: Party, slate, ticket

**List representative** (UI: "Apoderado"):
The Roster member who presents a List to the Electoral commission on its behalf.
_Avoid_: Sponsor, delegate, list leader

**Candidate** (UI: "Candidato"):
A Roster member running for one Position on exactly one List.
_Avoid_: Nominee, runner

**Submission period** (UI: "Presentación de listas"):
The window of an Election during which Lists are presented and approved or rejected.
_Avoid_: Registration, nomination window

**Ballot** (UI: "Voto"):
One voter's choice in an Election: one whole List or a Blank vote.
_Avoid_: Vote record, response

**Blank vote** (UI: "Voto en blanco"):
A Ballot that chooses no List; it is counted but never wins.
_Avoid_: Null vote, abstention, empty vote

**Voter registry** (UI: "Registro de votantes"):
The record of which Roster members have already voted in an Election, kept apart from the Ballot box.
_Avoid_: Attendance, voter log

**Ballot box** (UI: "Urna"):
The store of an Election's Ballots, holding nothing that links a Ballot to its voter.
_Avoid_: Votes table, results

**Secret ballot** (UI: "Voto secreto"):
The Election mode in which no one, including School authorities, can learn how a Roster member voted.
_Avoid_: Anonymous vote, private vote

**Nominal vote** (UI: "Voto nominal"):
An Election mode in which each Ballot is recorded with the name of its voter; planned for later.
_Avoid_: Open vote, public vote, roll call

**Turnout** (UI: "Participación"):
How many members of the Electorate voted, out of its total; never who voted.
_Avoid_: Attendance, quorum

**Annulment** (UI: "Anulación"):
The cancellation of an Election, with a reason visible to everyone; an annulled Election publishes no result and never reopens.
_Avoid_: Cancellation, deletion, void
