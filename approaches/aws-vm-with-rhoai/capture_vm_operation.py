# This project was developed with assistance from AI tools.
"""Read executive completion evidence through the existing cached VM SDK."""

import argparse
import json
from pathlib import Path
import subprocess


def capture(workspace, helper_directory, output):
    entry = workspace / "bazel-bin/src"
    stage = entry / "_omts_app_stage2_bootstrap.py"
    runfiles = entry / "omts_app.runfiles"
    python = next(runfiles.glob("*python_3_11*/bin/python3"))
    launcher = f"""
import os,sys,json,io,contextlib
from pathlib import Path
os.environ['RUNFILES_DIR']={str(runfiles)!r}
stage=Path({str(stage)!r})
sys.argv=[str(stage)]
namespace={{'__file__':str(stage),'__name__':'hybrid_proof_bootstrap'}}
parts=stage.read_text().rsplit('\\nmain()',1)
if len(parts)!=2:raise RuntimeError('Unsupported cached SDK bootstrap')
exec(compile(parts[0],str(stage),'exec'),namespace)
def invoke(*args,**kwargs):
 with contextlib.redirect_stdout(io.StringIO()),contextlib.redirect_stderr(io.StringIO()):
  sys.path.insert(0,{str(helper_directory)!r})
  import collision_probe
  from intrinsic.solutions import deployments
  solution=deployments.connect(address='localhost:17080')
  evidence=collision_probe.collect(solution)
 print('HYBRID_OPERATION_PROOF='+json.dumps(evidence))
namespace['_run_py_path']=invoke
namespace['main']()
"""
    result = subprocess.run(
        [str(python), "-"],
        input=launcher,
        capture_output=True,
        text=True,
        cwd=workspace,
        timeout=60,
    )
    lines = [
        line.split("=", 1)[1]
        for line in result.stdout.splitlines()
        if line.startswith("HYBRID_OPERATION_PROOF=")
    ]
    if result.returncode or len(lines) != 1:
        error = output.with_suffix(".error.log")
        error.write_text(result.stdout + result.stderr)
        error.chmod(0o600)
        raise RuntimeError(
            "Executive proof collection failed; private details retained"
        )
    data = json.loads(lines[0])
    output.write_text(json.dumps(data, indent=2) + "\n")
    output.chmod(0o600)
    print(
        json.dumps(
            {
                key: data[key]
                for key in [
                    "operation_done",
                    "operation_state",
                    "cycle_completed",
                    "stages",
                    "failure",
                ]
            }
        )
    )
    return data


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("workspace", "helper-directory", "output"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
    capture(
        args.workspace.expanduser(),
        args.helper_directory.expanduser(),
        args.output.expanduser(),
    )
