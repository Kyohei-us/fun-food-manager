export async function CheckCurrentUser() {
    const res = await fetch('/api/is-logged-in', { 
            method: 'GET' 
        });
    const currUser = await res.json();
    if (currUser.currentUserEmail) {
      return currUser.currentUserEmail
    } else {
      return ""
    }
  }
