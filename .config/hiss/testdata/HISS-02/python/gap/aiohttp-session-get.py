"""Gap: aiohttp.ClientSession requests without timeout= are not tracked by the Python I/O scanner."""

import aiohttp


async def fetch_aiohttp(url):
    """Fetch url through aiohttp.ClientSession.get without a deadline."""
    async with aiohttp.ClientSession() as session:
        async with session.get(url) as resp:
            return await resp.text()
