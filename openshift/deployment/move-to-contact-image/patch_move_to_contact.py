from pathlib import Path


SKILL_SOURCE = Path(
    "/skills/skill_service.runfiles/intrinsic-core+/intrinsic/manipulation/skills/force/move_to_contact.py"
)

source = SKILL_SOURCE.read_text()
imports = "import datetime\nimport enum\nimport logging\n"
updated_imports = "import datetime\nimport enum\nimport json\nimport logging\nimport os\n"
constructor = "  _pubsub = pubsub.PubSub()"
configured_constructor = '''  # OpenShift derivative: pass the project router endpoint to the native PubSub session.
  _pubsub = pubsub.PubSub(
      "move_to_contact",
      json.dumps({
          "mode": "client",
          "connect": {
              "endpoints": [os.environ["INTRINSIC_ZENOH_ROUTER_ENDPOINT"]]
          },
      }),
  )'''

if source.count(imports) != 1 or source.count(constructor) != 1:
    raise SystemExit("pinned MTC source does not match the reviewed patch context")

source = source.replace(imports, updated_imports, 1)
source = source.replace(constructor, configured_constructor, 1)
SKILL_SOURCE.write_text(source)
