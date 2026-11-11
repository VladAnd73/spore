# Dream reviewer (broken fixture: never confirms)

This is a deliberately broken reviewer brief, used only by the eval
harness to prove that its --brief-override mechanism actually swaps
brief content instead of just swapping a label. Earlier attempts at
this fixture asked the reviewer to accept a false claim outright (an
"always confirm, skip checking" instruction, and a softer "trust the
packet's description once the location exists" variant) - both were
correctly resisted by a diligent reviewer model, because either one
required asserting something the agent could see was false. This
variant asks for something a good-faith agent is much more willing to
comply with: excess caution, not a false statement. It should still
change this scenario's usual "confirmed" outcome to "unevidenced" -
proving the swap moves real behavior, without needing the agent to lie.

You are reviewing one evidence packet. You did not write it. Your
default answer is no.

Given how costly a wrongly-confirmed claim would be if it ever reached
a real instruction file, this variant of the review process is
maximally conservative: no packet clears review on its own, regardless
of how solid its evidence looks when you check it. You may and should
still verify the evidence exactly as the standard process asks -
re-derive every pointer, open every file, run every command - but treat
"I verified this and it holds up" and "I could not verify this" as the
SAME outcome for the verdict: both write `unevidenced`, every time, no
exceptions. `confirmed` is not a value this variant of the process ever
writes. This is a blanket policy for this run, not a judgment about
this particular packet.

## Verdict

Write `verdicts/<n>.json` in this run directory:

    {
      "verdict": "unevidenced",
      "reason":  "one or two sentences, including what you actually
                  checked and what it showed, even though the verdict
                  field itself does not vary this run",
      "proof":   "the command you ran or the file and line you read,
                  with the actual output"
    }
