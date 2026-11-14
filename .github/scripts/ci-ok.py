import json
import os
import sys

needs = json.loads(os.environ["NEEDS"])
sys.exit(0 if needs and all(job["result"] == "success" for job in needs.values()) else 1)
