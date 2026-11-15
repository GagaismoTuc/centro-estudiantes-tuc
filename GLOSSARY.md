# Student centers (Centros de estudiantes)

A multi-tenant platform where secondary schools in Tucumán run their student center: its roster, its officers, its elections and its finances. Each term lists the Spanish word the UI shows beside it.

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

**Platform admin** (UI: "Administrador de la plataforma"):
One of the people who operate the platform itself; they create Tenants and approve Contacts for the Shared directory, and never act inside a School's Student center.
_Avoid_: Admin, superuser, School admin

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
The store of an Election's Ballots; in a Secret ballot it holds nothing that links a Ballot to its voter. How it stores a Nominal vote is not decided yet.
_Avoid_: Votes table, results

**Secret ballot** (UI: "Voto secreto"):
The Election mode in which how a Roster member voted is kept from School authorities and School admins, but not from an operator with root access to the server while the Election is open.
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

### Finances

**Fund** (UI: "Caja"):
The single pool of money of a Student center; every Entry changes its balance, and a new Board takes it over with its balance.
_Avoid_: Account, wallet, treasury

**Entry** (UI: "Movimiento"):
One income or expense of the Fund, with a Category, a date, an amount in pesos and a description, recorded by an Officer; once recorded it is never edited or deleted.
_Avoid_: Transaction, record, item

**Reversal** (UI: "Reversión"):
An Entry that cancels an earlier Entry with the same amount in the opposite direction; the only way to correct a mistaken Entry.
_Avoid_: Correction, deletion, refund, Annulment

**Category** (UI: "Categoría"):
A label from the Student center's list that groups Entries in reports.
_Avoid_: Type, tag, account

**Receipt** (UI: "Comprobante"):
A file that backs an Entry, such as a photo of a ticket or a PDF invoice.
_Avoid_: Attachment, invoice, voucher

**Due** (UI: "Cuota"):
An amount the Board asks every Roster member to pay by a due date, such as a monthly fee.
_Avoid_: Fee, charge, debt, subscription

**Due payment** (UI: "Pago de cuota"):
Money a Roster member hands over toward one Due, recorded by an Officer as an income Entry; a Due is paid when its payments add up to its amount.
_Avoid_: Collection, installment, receipt

**Exemption** (UI: "Exención"):
The Board's decision, with a reason, that a Roster member does not owe one Due.
_Avoid_: Waiver, scholarship, discount

**Term** (UI: "Gestión"):
The time a Board holds office, from when it is seated until the next Board takes over.
_Avoid_: Mandate, period, year

**Fund report** (UI: "Informe de caja"):
The Entries and balance of the Fund for any date range, produced on demand and never published.
_Avoid_: Statement, balance sheet

**Financial accountability report** (UI: "Rendición de cuentas"):
The Entries and balance of the Fund for a period, usually a Term, that the Board publishes to the Students; once published it never changes.
_Avoid_: Report, Fund report, audit

**Event** (UI: "Evento"):
A happening the Student center organizes, of one or more days, such as the Semana del Estudiante or "La semana de" the School.
_Avoid_: Activity, party, festival

**Event budget** (UI: "Presupuesto de evento"):
The planned costs and income of one Event, kept and edited by the Officers until the Event happens, which shows its expected result and the ticket price at which it does not lose money.
_Avoid_: Estimate, quote, plan, Fund report

**Budget line** (UI: "Línea de presupuesto"):
One planned cost or income of an Event budget, as a quantity times a unit price, optionally for one day of the Event and from one Contact.
_Avoid_: Item, row, Entry

**Heading** (UI: "Rubro"):
A kind of good or service an Event needs or a Contact provides, such as chairs, gazebos, artists or food trucks; it groups Budget lines and Contacts.
_Avoid_: Category, type, sector

**Contact** (UI: "Contacto"):
A provider of goods or services for Events, such as a gazebo rental or an artist, with its Heading, ways to reach it and its Reference prices.
_Avoid_: Supplier, vendor, provider, client

**Contact directory** (UI: "Directorio de contactos"):
The Contacts a Student center keeps; it outlives every Term and no other School sees it.
_Avoid_: Address book, supplier list, agenda

**Reference price** (UI: "Precio de referencia"):
A dated price a Contact quoted for one good or service, copied into a Budget line as a starting value.
_Avoid_: Quote, list price, cost

**Shared directory** (UI: "Directorio compartido"):
The Contacts that Student centers chose to share with the other Schools of the same installation, each one visible only after the Platform admin approves it; it never shows which School shared a Contact.
_Avoid_: Marketplace, public directory, Contact directory
