---------------------------- MODULE CopyBack ----------------------------
(* bunk's promise about work: what an agent did in a box is never thrown
   away without --force, and the host's branch moves only onto the commit
   the box started from.

   One directory's box, and the commands that touch it: `bunk run` (Runs),
   `bunk rm --dir DIR`, `bunk gc`, and the user moving the branch on the
   host. `bunk rm NAME` and `bunk rm --dir DIR --force` are left out: they
   delete on purpose.

   Each action names the code it describes. Every command finds the box by
   its path, so a command that started against a box that was since removed
   and made again acts on the new one, as the code would.

   See .claude/skills/verify-copy-back/SKILL.md. *)
EXTENDS Naturals

CONSTANTS
  Runs,          \* the `bunk run` processes for the directory
  MaxHostMoves,  \* how often the user moves the branch on the host
  KillRuns,      \* a bunk run can die at any step (kill, ^C during the copy back, a host crash)
  VMStops,       \* the box's VM can stop by itself (a host reboot, a VM crash)
  GcRaces        \* gc runs while a bunk run or bunk rm --dir is partway through,
                 \* and they take steps between its look at a box and its removal

VARIABLES
  exists,    \* the box's directory exists                    (Box.Exists)
  running,   \* its supervisor holds the box's lock           (Box.Running)
  unsynced,  \* its unsynced file exists: task.Run writes it before the
             \* command and removes it after the copy back
  lock,      \* who holds its run.lock                        (lockRun)
  hostRef,   \* the commit the host's branch is on, as a count of moves
  pc,        \* where each bunk run is
  created,   \* task.Run's `created`
  copied,    \* the host's branch as the run's rsync gave it to the box
  before,    \* copyIn's `refs`: the host's branch as the run read it
  rmPc,      \* where bunk rm --dir is
  gcPc,      \* where bunk gc is
  held,      \* not in the code: the box holds work that isn't home. True from
             \* when a command runs in the box until a copy back finishes.
  harm       \* not in the code: the rules broken so far

vars == <<exists, running, unsynced, lock, hostRef, pc, created, copied,
          before, rmPc, gcPc, held, harm>>
box  == <<exists, running, unsynced, lock, held>>

None == "none"

Init ==
  /\ exists = FALSE /\ running = FALSE /\ unsynced = FALSE
  /\ lock = None /\ hostRef = 0
  /\ pc = [r \in Runs |-> "start"]
  /\ created = [r \in Runs |-> FALSE]
  /\ copied = [r \in Runs |-> 0]
  /\ before = [r \in Runs |-> 0]
  /\ rmPc = "start" /\ gcPc = "look"
  /\ held = FALSE /\ harm = {}

Go(r, to) == pc' = [pc EXCEPT ![r] = to]

\* Box.Remove: Stop, then os.RemoveAll. The run.lock file goes with the
\* directory, so a process that held it now holds a lock on nothing.
RemoveBox(rule) ==
  /\ exists' = FALSE /\ running' = FALSE /\ unsynced' = FALSE
  /\ lock' = None /\ held' = FALSE
  /\ harm' = IF held THEN harm \cup {rule} ELSE harm

\* task.Run returns after lockRun: the deferred unlock.
Leave(r) ==
  /\ Go(r, "done")
  /\ lock' = IF lock = r THEN None ELSE lock

\* task.Run's os.WriteFile(unsynced, err...) after copyOut fails, which
\* itself fails if the directory is gone. Then it returns.
LeaveHolding(r) ==
  /\ Leave(r)
  /\ unsynced' = exists

----------------------------------------------------------------------------
(* bunk run: task.Run *)

\* `if !b.Exists()`
RunLooks(r) ==
  /\ pc[r] = "start"
  /\ Go(r, IF exists THEN "lock" ELSE "create")
  /\ UNCHANGED <<box, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* b.Create, which renames the new box into place or fails if one is there.
RunCreates(r) ==
  /\ pc[r] = "create"
  /\ IF exists
       THEN Go(r, "done") /\ UNCHANGED <<exists, created>>
       ELSE Go(r, "lock") /\ exists' = TRUE
            /\ created' = [created EXCEPT ![r] = TRUE]
  /\ UNCHANGED <<running, unsynced, lock, held, hostRef, copied, before, rmPc, gcPc, harm>>

\* lockRun. Opening run.lock fails if the box is gone.
RunLocks(r) ==
  /\ pc[r] = "lock"
  /\ IF exists /\ lock = None
       THEN Go(r, "up") /\ lock' = r
       ELSE Go(r, "done") /\ UNCHANGED lock
  /\ UNCHANGED <<exists, running, unsynced, held, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* b.Up succeeds: the box is running and ready.
RunUp(r) ==
  /\ pc[r] = "up"
  /\ exists
  /\ running' = TRUE
  /\ Go(r, "marker")
  /\ UNCHANGED <<exists, unsynced, lock, held, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* b.Up fails without starting the box (the limit on running boxes, a
\* supervisor that exits), then `if created && !b.Running() { b.Remove() }`.
RunUpFails(r) ==
  /\ pc[r] = "up"
  /\ IF created[r] /\ exists /\ ~running
       THEN RemoveBox("RunNeverRemovesHeldWork") /\ Go(r, "done")
       ELSE Leave(r) /\ UNCHANGED <<exists, running, unsynced, held, harm>>
  /\ UNCHANGED <<hostRef, created, copied, before, rmPc, gcPc>>

\* b.Up starts the box and then fails (it never gets ready). The box is
\* running, so it stays.
RunUpFailsLate(r) ==
  /\ pc[r] = "up"
  /\ exists
  /\ running' = TRUE
  /\ Leave(r)
  /\ UNCHANGED <<exists, unsynced, held, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* `if why, err := os.ReadFile(unsynced); err == nil { return ... }`
RunChecksMarker(r) ==
  /\ pc[r] = "marker"
  /\ IF unsynced
       THEN Leave(r) /\ UNCHANGED <<exists, running, unsynced, held>>
       ELSE Go(r, "refs") /\ UNCHANGED box
  /\ UNCHANGED <<hostRef, created, copied, before, rmPc, gcPc, harm>>

\* The start of copyIn: `refs[b], _ = git(w.root, "rev-parse", ...)`, before
\* anything is copied.
RunReadsRefs(r) ==
  /\ pc[r] = "refs"
  /\ before' = [before EXCEPT ![r] = hostRef]
  /\ Go(r, "copyin")
  /\ UNCHANGED <<box, hostRef, created, copied, rmPc, gcPc, harm>>

\* copyIn's rsync --delete of the worktree and repository into the box.
RunCopiesIn(r) ==
  /\ pc[r] = "copyin"
  /\ exists /\ running
  /\ harm' = IF held THEN harm \cup {"RunNeverCopiesOverHeldWork"} ELSE harm
  /\ copied' = [copied EXCEPT ![r] = hostRef]
  /\ Go(r, "hold")
  /\ UNCHANGED <<box, hostRef, created, before, rmPc, gcPc>>

\* The agent's credential can't be given or taken before copyIn, or copyIn
\* fails (the box is down, or an rsync or ssh fails, in the repository copy or
\* the config copies after it). Run returns without writing unsynced.
RunCopyInFails(r) ==
  /\ pc[r] \in {"refs", "copyin", "hold"}
  /\ Leave(r)
  /\ UNCHANGED <<exists, running, unsynced, held, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* `os.WriteFile(unsynced, "the bunk run that made it was interrupted")`,
\* before the command. If the write fails, Run returns.
RunMarksHeld(r) ==
  /\ pc[r] = "hold"
  /\ IF exists
       THEN Go(r, "command") /\ unsynced' = TRUE /\ UNCHANGED lock
       ELSE Leave(r) /\ UNCHANGED unsynced
  /\ UNCHANGED <<exists, running, held, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* s.interactive: the command runs in the box, if the box is there.
RunCommand(r) ==
  /\ pc[r] = "command"
  /\ held' = IF exists /\ running THEN TRUE ELSE held
  /\ Go(r, "branch")
  /\ UNCHANGED <<exists, running, unsynced, lock, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* copyOut as far as `git update-ref branch sha old`, which git applies only
\* if the host's branch is still `old`.
RunMovesBranch(r) ==
  /\ pc[r] = "branch"
  /\ exists /\ running
  /\ hostRef = before[r]
  /\ harm' = IF hostRef # copied[r] THEN harm \cup {"BranchMovesOnlyFromWhatTheBoxCopied"} ELSE harm
  /\ Go(r, "worktree")
  /\ UNCHANGED <<box, hostRef, created, copied, before, rmPc, gcPc>>

\* copyOut's rsync of the working tree, HEAD, and index. The work is home.
\* The work coming home and `os.Remove(unsynced)` are one step here: a run
\* that dies between them leaves a box held with nothing in it to lose.
RunCopiesBack(r) ==
  /\ pc[r] = "worktree"
  /\ exists /\ running
  /\ held' = FALSE
  /\ unsynced' = FALSE
  /\ Leave(r)
  /\ UNCHANGED <<exists, running, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* copyOut fails: the branch moved, the box is down, or an rsync fails. Run
\* writes unsynced and returns.
RunCopyOutFails(r) ==
  /\ pc[r] \in {"branch", "worktree"}
  /\ LeaveHolding(r)
  /\ UNCHANGED <<exists, running, held, hostRef, created, copied, before, rmPc, gcPc, harm>>

\* The process dies. The kernel drops its lock; nothing else is cleaned up.
RunKilled(r) ==
  /\ KillRuns
  /\ pc[r] \notin {"start", "done"}
  /\ Go(r, "done")
  /\ lock' = IF lock = r THEN None ELSE lock
  /\ UNCHANGED <<exists, running, unsynced, held, hostRef, created, copied, before, rmPc, gcPc, harm>>

----------------------------------------------------------------------------
(* bunk rm --dir DIR: task.Remove without force. It can run again. *)

\* `if !b.Exists() { return nil }`
RmLooks ==
  /\ rmPc = "start" /\ exists
  /\ rmPc' = "lock"
  /\ UNCHANGED <<box, hostRef, pc, created, copied, before, gcPc, harm>>

\* lockRun
RmLocks ==
  /\ rmPc = "lock"
  /\ IF exists /\ lock = None
       THEN rmPc' = "marker" /\ lock' = "rm"
       ELSE rmPc' = "start" /\ UNCHANGED lock
  /\ UNCHANGED <<exists, running, unsynced, held, hostRef, pc, created, copied, before, gcPc, harm>>

\* `if why, err := os.ReadFile(unsynced); err == nil { return ... }`
RmChecksMarker ==
  /\ rmPc = "marker"
  /\ IF unsynced
       THEN rmPc' = "start" /\ lock' = IF lock = "rm" THEN None ELSE lock
       ELSE rmPc' = "remove" /\ UNCHANGED lock
  /\ UNCHANGED <<exists, running, unsynced, held, hostRef, pc, created, copied, before, gcPc, harm>>

\* b.Remove
RmRemoves ==
  /\ rmPc = "remove"
  /\ rmPc' = "start"
  /\ IF exists
       THEN RemoveBox("RmDirNeverRemovesHeldWork")
       ELSE UNCHANGED box /\ UNCHANGED harm
  /\ UNCHANGED <<hostRef, pc, created, copied, before, gcPc>>

----------------------------------------------------------------------------
(* bunk gc: box.GC, for this box. It can run again. *)

\* `if b.Running() { ...; continue }` and `if exists(b.path("unsynced")) {
\* continue }`, with the box old enough for gc (unused for its age, or its
\* directory gone). Unless GcRaces, gc runs while no bunk run or bunk rm --dir
\* is partway through, and the b.Remove that follows is part of the same step.
Quiet == rmPc = "start" /\ \A r \in Runs : pc[r] \in {"start", "done"}

GcLooks ==
  /\ gcPc = "look"
  /\ exists /\ ~running /\ ~unsynced
  /\ IF GcRaces
       THEN gcPc' = "remove" /\ UNCHANGED <<box, harm>>
       ELSE Quiet /\ RemoveBox("GcNeverRemovesHeldWork") /\ UNCHANGED gcPc
  /\ UNCHANGED <<hostRef, pc, created, copied, before, rmPc>>

\* b.Remove, which stops the box if it's running by now.
GcRemoves ==
  /\ gcPc = "remove"
  /\ gcPc' = "look"
  /\ IF exists
       THEN RemoveBox("GcNeverRemovesHeldWork")
       ELSE UNCHANGED box /\ UNCHANGED harm
  /\ UNCHANGED <<hostRef, pc, created, copied, before, rmPc>>

----------------------------------------------------------------------------
(* What happens around bunk *)

\* The VM stops and its supervisor exits. The disk keeps what the box held.
VMStopsByItself ==
  /\ VMStops /\ running
  /\ running' = FALSE
  /\ UNCHANGED <<exists, unsynced, lock, held, hostRef, pc, created, copied, before, rmPc, gcPc, harm>>

\* The user moves the branch on the host, at any moment: a commit, a reset,
\* a fetch.
HostMovesBranch ==
  /\ hostRef < MaxHostMoves
  /\ hostRef' = hostRef + 1
  /\ UNCHANGED <<box, pc, created, copied, before, rmPc, gcPc, harm>>

\* Nothing is left to do.
Finished ==
  /\ \A r \in Runs : pc[r] = "done"
  /\ UNCHANGED vars

Next ==
  \/ \E r \in Runs :
       \/ RunLooks(r) \/ RunCreates(r) \/ RunLocks(r)
       \/ RunUp(r) \/ RunUpFails(r) \/ RunUpFailsLate(r)
       \/ RunChecksMarker(r) \/ RunCopiesIn(r) \/ RunCopyInFails(r)
       \/ RunReadsRefs(r) \/ RunMarksHeld(r) \/ RunCommand(r)
       \/ RunMovesBranch(r) \/ RunCopiesBack(r) \/ RunCopyOutFails(r)
       \/ RunKilled(r)
  \/ RmLooks \/ RmLocks \/ RmChecksMarker \/ RmRemoves
  \/ GcLooks \/ GcRemoves
  \/ VMStopsByItself \/ HostMovesBranch
  \/ Finished

Spec == Init /\ [][Next]_vars

----------------------------------------------------------------------------
(* The rules. `harm` gets a rule's name at the step that breaks it. *)

\* bunk run never copies the host's files over work the box still holds.
RunNeverCopiesOverHeldWork == "RunNeverCopiesOverHeldWork" \notin harm

\* bunk run's cleanup after a failed start never deletes a box that holds work.
RunNeverRemovesHeldWork == "RunNeverRemovesHeldWork" \notin harm

\* bunk rm --dir without --force never deletes a box that holds work.
RmDirNeverRemovesHeldWork == "RmDirNeverRemovesHeldWork" \notin harm

\* bunk gc never deletes a box that holds work.
GcNeverRemovesHeldWork == "GcNeverRemovesHeldWork" \notin harm

\* The copy back moves the host's branch only if it is still on the commit
\* the box started from.
BranchMovesOnlyFromWhatTheBoxCopied == "BranchMovesOnlyFromWhatTheBoxCopied" \notin harm
=============================================================================
