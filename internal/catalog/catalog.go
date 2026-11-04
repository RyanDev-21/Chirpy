// Package catalog holds the fixed lists users pick from on their profile:
// places (no coordinates) and interests.
package catalog

import "strings"

// Place is a city, a country ("other city in ...") or a world region.
// Users pick one; no coordinates are ever stored.
type Place struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country,omitempty"` // ISO code; empty for world regions
}

// Country groups the places of one Asian country for the picker.
type Country struct {
	Code   string  `json:"code"`
	Name   string  `json:"name"`
	Places []Place `json:"places"`
}

// Interest is one entry of the fixed interest list.
type Interest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// MaxInterests is how many interests a user may pick.
const MaxInterests = 10

var asia = []struct {
	code, name string
	cities     []string
}{
	{"th", "Thailand", []string{"Bangkok", "Chiang Mai", "Chiang Rai", "Phuket", "Pattaya", "Khon Kaen", "Hat Yai", "Nakhon Ratchasima", "Udon Thani", "Hua Hin", "Ayutthaya", "Nonthaburi", "Surat Thani", "Ubon Ratchathani", "Mae Sot"}},
	{"mm", "Myanmar", []string{"Yangon", "Mandalay", "Naypyidaw", "Mawlamyine", "Bago", "Taunggyi", "Pathein", "Monywa", "Myitkyina", "Sittwe", "Lashio", "Dawei", "Myeik", "Hpa-An", "Magway", "Pyay"}},
	{"la", "Laos", []string{"Vientiane", "Luang Prabang", "Pakse", "Savannakhet"}},
	{"kh", "Cambodia", []string{"Phnom Penh", "Siem Reap", "Battambang", "Sihanoukville"}},
	{"vn", "Vietnam", []string{"Hanoi", "Ho Chi Minh City", "Da Nang", "Hai Phong", "Can Tho", "Nha Trang", "Hue"}},
	{"my", "Malaysia", []string{"Kuala Lumpur", "Penang", "Johor Bahru", "Ipoh", "Kota Kinabalu", "Kuching", "Malacca"}},
	{"sg", "Singapore", []string{"Singapore"}},
	{"id", "Indonesia", []string{"Jakarta", "Surabaya", "Bandung", "Medan", "Bali", "Yogyakarta", "Semarang", "Makassar"}},
	{"ph", "Philippines", []string{"Manila", "Quezon City", "Cebu", "Davao"}},
	{"bn", "Brunei", []string{"Bandar Seri Begawan"}},
	{"cn", "China", []string{"Beijing", "Shanghai", "Guangzhou", "Shenzhen", "Chengdu", "Chongqing", "Kunming", "Hangzhou", "Wuhan", "Xi'an"}},
	{"hk", "Hong Kong", []string{"Hong Kong"}},
	{"mo", "Macau", []string{"Macau"}},
	{"tw", "Taiwan", []string{"Taipei", "Taichung", "Kaohsiung"}},
	{"jp", "Japan", []string{"Tokyo", "Osaka", "Kyoto", "Yokohama", "Nagoya", "Fukuoka", "Sapporo"}},
	{"kr", "South Korea", []string{"Seoul", "Busan", "Incheon", "Daegu"}},
	{"mn", "Mongolia", []string{"Ulaanbaatar"}},
	{"in", "India", []string{"Delhi", "Mumbai", "Bengaluru", "Chennai", "Kolkata", "Hyderabad", "Pune"}},
	{"bd", "Bangladesh", []string{"Dhaka", "Chittagong"}},
	{"np", "Nepal", []string{"Kathmandu", "Pokhara"}},
	{"lk", "Sri Lanka", []string{"Colombo", "Kandy"}},
	{"pk", "Pakistan", []string{"Karachi", "Lahore", "Islamabad"}},
	{"bt", "Bhutan", []string{"Thimphu"}},
	{"mv", "Maldives", []string{"Malé"}},
}

// Regions outside the detailed Asian list.
var regions = []Place{
	{ID: "other-asia", Name: "Elsewhere in Asia"},
	{ID: "middle-east", Name: "Middle East"},
	{ID: "europe", Name: "Europe"},
	{ID: "north-america", Name: "North America"},
	{ID: "latin-america", Name: "Latin America"},
	{ID: "africa", Name: "Africa"},
	{ID: "oceania", Name: "Oceania"},
}

var interests = []Interest{
	{"gaming", "Video games", "Games"}, {"mobile_games", "Mobile games", "Games"}, {"esports", "Esports", "Games"}, {"board_games", "Board games", "Games"},
	{"football", "Football", "Sports"}, {"basketball", "Basketball", "Sports"}, {"badminton", "Badminton", "Sports"}, {"volleyball", "Volleyball", "Sports"},
	{"sepak_takraw", "Sepak takraw", "Sports"}, {"martial_arts", "Martial arts", "Sports"}, {"swimming", "Swimming", "Sports"}, {"running", "Running", "Sports"},
	{"cycling", "Cycling", "Sports"}, {"gym", "Gym & fitness", "Sports"},
	{"music", "Music", "Music"}, {"kpop", "K-pop", "Music"}, {"jpop", "J-pop", "Music"}, {"rock", "Rock", "Music"}, {"hiphop", "Hip-hop", "Music"},
	{"edm", "EDM", "Music"}, {"instruments", "Playing instruments", "Music"}, {"singing", "Singing", "Music"},
	{"movies", "Movies", "Entertainment"}, {"tv_series", "TV series", "Entertainment"}, {"kdrama", "K-drama", "Entertainment"}, {"anime", "Anime", "Entertainment"},
	{"manga", "Manga & comics", "Entertainment"},
	{"art", "Art & drawing", "Creative"}, {"photography", "Photography", "Creative"}, {"writing", "Writing", "Creative"}, {"design", "Design", "Creative"},
	{"fashion", "Fashion", "Creative"}, {"makeup", "Makeup & beauty", "Creative"},
	{"coding", "Coding", "Tech"}, {"tech", "Technology", "Tech"}, {"ai", "AI", "Tech"}, {"gadgets", "Gadgets", "Tech"},
	{"travel", "Travel", "Lifestyle"}, {"food", "Food", "Lifestyle"}, {"cooking", "Cooking", "Lifestyle"}, {"coffee", "Coffee & cafés", "Lifestyle"},
	{"pets", "Pets", "Lifestyle"}, {"nature", "Nature", "Lifestyle"}, {"hiking", "Hiking & camping", "Lifestyle"}, {"meditation", "Meditation", "Lifestyle"},
	{"reading", "Reading", "Learning"}, {"languages", "Languages", "Learning"}, {"science", "Science", "Learning"}, {"history", "History", "Learning"},
	{"business", "Business", "Learning"}, {"volunteering", "Volunteering", "Learning"},
}

var (
	countries  []Country
	placeByID  = map[string]Place{}
	interestOK = map[string]bool{}
)

func slug(s string) string {
	s = strings.ToLower(strings.NewReplacer(" ", "-", "'", "", "é", "e").Replace(s))
	return s
}

func init() {
	for _, c := range asia {
		country := Country{Code: c.code, Name: c.name}
		for _, city := range c.cities {
			country.Places = append(country.Places, Place{ID: c.code + "-" + slug(city), Name: city, Country: c.code})
		}
		if len(c.cities) > 1 {
			country.Places = append(country.Places, Place{ID: c.code, Name: "Other city in " + c.name, Country: c.code})
		}
		for _, p := range country.Places {
			placeByID[p.ID] = p
		}
		countries = append(countries, country)
	}
	for _, r := range regions {
		placeByID[r.ID] = r
	}
	for _, i := range interests {
		interestOK[i.ID] = true
	}
}

// LookupPlace returns the place with that id.
func LookupPlace(id string) (Place, bool) {
	p, ok := placeByID[id]
	return p, ok
}

// ValidInterest reports whether id is on the interest list.
func ValidInterest(id string) bool { return interestOK[id] }

// Options is everything the profile editor offers.
type Options struct {
	Countries []Country  `json:"countries"`
	Regions   []Place    `json:"regions"`
	Interests []Interest `json:"interests"`
	// MaxInterests is how many interests may be picked.
	MaxInterests int `json:"max_interests"`
}

// AllOptions lists places and interests for the profile editor.
func AllOptions() Options {
	return Options{Countries: countries, Regions: regions, Interests: interests, MaxInterests: MaxInterests}
}
