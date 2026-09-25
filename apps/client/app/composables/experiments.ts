/**
 * The project's experiments and every write to them: create, start, ramp, stop, finish, and
 * ship a winner, each made by the API (stores/experiments.ts), which keeps the experiment's own
 * history of who did what. A write resolves with the API's refusal in its words, or null.
 *
 * Shipping a winner is the one write that reaches past the experiment: the API makes the
 * winning value the flag's fallback and drops the rules that split players between arms, as an
 * edit to the draft, to be published like any other.
 */
export function useExperiments() {
  const store = useExperimentsStore()
  const { experiments, loading, error } = storeToRefs(store)
  const live = useLiveConfigStore()

  void store.load()

  const experimentById = (id: string) => experiments.value.find(entry => entry.id === id) ?? null

  /** A new test in draft: its id, or why the API refused it. */
  async function create(draft: ExperimentDraft): Promise<{ id: string | null, problem: string | null }> {
    const out = await store.create(draft)
    return { id: out.experiment?.id ?? null, problem: out.problem }
  }

  const problemOf = async (write: Promise<{ problem: string | null }>) => (await write).problem

  const start = (id: string) => problemOf(store.start(id))
  const setTraffic = (id: string, traffic: number) => problemOf(store.setTraffic(id, traffic))
  const stop = (id: string, reason: string) => problemOf(store.stop(id, reason))
  /** Ends a test with a decision: a winning arm, or null for none. */
  const finish = (id: string, winner: string | null, conclusion: string) => problemOf(store.conclude(id, winner, conclusion))

  /** Makes the winner the flag's answer for everybody; the flag is read again after. */
  async function ship(id: string): Promise<string | null> {
    const problem = await problemOf(store.ship(id))
    if (!problem) await live.load(true)
    return problem
  }

  return { experiments, loading, error, reload: () => store.load(true), resultsFor: store.resultsFor, experimentById, create, start, setTraffic, stop, finish, ship }
}
