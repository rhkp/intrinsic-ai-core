# This project was developed with assistance from AI tools.
PYTHON ?= .venv/bin/python
BOOTSTRAP_PYTHON ?= python3

.PHONY: setup-validation validate test-python test-go lint-python lint-go lint-shell lint-yaml scan-secrets validate-config validate-provenance check-whitespace

setup-validation:
	$(BOOTSTRAP_PYTHON) scripts/setup_validation.py

validate test-python test-go lint-python lint-go lint-shell lint-yaml scan-secrets validate-config validate-provenance check-whitespace:
	@test -x "$(PYTHON)" || { echo "Run make setup-validation first (Python 3.12+, Go, Git and curl required)."; exit 1; }
	$(PYTHON) scripts/validation.py $@
