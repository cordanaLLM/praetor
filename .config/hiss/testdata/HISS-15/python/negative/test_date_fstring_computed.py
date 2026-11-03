import datetime
base = datetime.date(2026, 11, 1)
expiry = f"{(base + datetime.timedelta(days=14)).isoformat()}"
