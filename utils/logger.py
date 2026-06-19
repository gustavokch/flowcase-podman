import time
from __init__ import db

def log(level: str, message: str):
	"""Log a message to the database and console.

	Degrades to console-only when there is no Flask application/DB context
	available (e.g. during container-engine initialization at startup, which
	runs outside a request/app context). This keeps logging from aborting its
	caller with "Working outside of application context".
	"""
	from models.log import Log

	timestamp = time.strftime('%Y-%m-%d %H:%M:%S')
	log_entry = None
	try:
		log_entry = Log(level=level, message=message)
		db.session.add(log_entry)
		db.session.commit()
		timestamp = log_entry.created_at.strftime('%Y-%m-%d %H:%M:%S')
	except Exception:
		# No app/DB context or the DB is unavailable — fall back to console only.
		try:
			db.session.rollback()
		except Exception:
			pass

	# Only print DEBUG logs if in debug mode
	try:
		from config.config import parse_args
		debug = parse_args().debug
	except Exception:
		debug = False

	if level != "DEBUG" or debug:
		print(f"[{level}] | {timestamp} | {message}", flush=True)

	return log_entry 